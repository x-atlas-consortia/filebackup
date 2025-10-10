package database

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL string

func New(ctx context.Context, dsn string, readonly bool) (*Database, error) {
	var lockFile *os.File
	var err error

	if !readonly {
		// Create and keep lock file open to prevent multiple writers
		lockFilePath := dsn + ".lock"
		lockFile, err = createLockFile(lockFilePath)
		if err != nil {
			return nil, fmt.Errorf("error creating lock file: %w", err)
		}
	}

	mode := "rwc"
	if readonly {
		mode = "ro"
	}

	fullDSN := dsn
	if strings.Contains(dsn, "?") {
		fullDSN += "&mode=" + mode
	} else {
		fullDSN += "?mode=" + mode
	}

	db, err := sql.Open("sqlite3", fullDSN)
	if err != nil {
		if lockFile != nil {
			lockFile.Close()
			os.Remove(lockFile.Name())
		}
		return nil, err
	}

	// Verify the connection
	err = db.Ping()
	if err != nil {
		db.Close()
		if lockFile != nil {
			lockFile.Close()
			os.Remove(lockFile.Name())
		}
		return nil, err
	}

	// Execute the schema SQL file (only for write mode)
	if !readonly {
		if _, err := db.Exec(schemaSQL); err != nil {
			db.Close()
			if lockFile != nil {
				lockFile.Close()
				os.Remove(lockFile.Name())
			}
			return nil, fmt.Errorf("error executing schema: %w", err)
		}
	}

	// Prepare commonly used statements
	fileExistsStmt, err := db.PrepareContext(ctx, `
        SELECT COUNT(path)
        FROM files
        WHERE path = ? AND size = ? AND last_modified_at = ?
	`)
	if err != nil {
		db.Close()
		if lockFile != nil {
			lockFile.Close()
			os.Remove(lockFile.Name())
		}
		return nil, fmt.Errorf("error preparing statements: %w", err)
	}

	sha256Stmt, err := db.PrepareContext(ctx, `
		SELECT sha256
		FROM files
		WHERE path = ? AND aws_version_id = ?
		LIMIT 1
	`)
	if err != nil {
		db.Close()
		if lockFile != nil {
			lockFile.Close()
			os.Remove(lockFile.Name())
		}
		return nil, fmt.Errorf("error preparing statements: %w", err)
	}

	return &Database{
		db:             db,
		fileExistsStmt: fileExistsStmt,
		lockFile:       lockFile,
		sha256Stmt:     sha256Stmt,
	}, nil
}

// createLockFile creates a lock file at the specified path to prevent multiple writers
func createLockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("another instance is already running (lock file exists: %s)", path)
		}
		return nil, fmt.Errorf("failed to create lock file: %w", err)
	}

	// Write process info to the lock file for debugging
	processInfo := fmt.Sprintf("PID: %d\nStarted: %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
	if _, err = file.WriteString(processInfo); err != nil {
		file.Close()
		os.Remove(path)
		return nil, fmt.Errorf("failed to write to lock file: %w", err)
	}

	// Flush to ensure data is written
	if err = file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return nil, fmt.Errorf("failed to sync lock file: %w", err)
	}

	return file, nil
}

// database holds the database connection
type Database struct {
	db             *sql.DB
	fileExistsStmt *sql.Stmt
	lockFile       *os.File
	sha256Stmt     *sql.Stmt
}

func (d *Database) Reindex(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `REINDEX`)
	if err != nil {
		return fmt.Errorf("error reindexing database: %w", err)
	}
	return nil
}

// Close closes the database connection and releases the lock
func (d *Database) Close() error {
	var dbErr, lockErr error

	// Close database connection
	if d.db != nil {
		dbErr = d.db.Close()
	}

	// Clean up lock file
	if d.lockFile != nil {
		lockFilePath := d.lockFile.Name()
		d.lockFile.Close()
		lockErr = os.Remove(lockFilePath)
	}

	// Return the first error encountered
	if dbErr != nil {
		return dbErr
	}
	return lockErr
}

type InsertEvent struct {
	Details   string
	EndedAt   int64
	StartedAt int64
	Type      string
}

func (d *Database) InsertEvent(ctx context.Context, event InsertEvent) error {
	stmt := `INSERT INTO events (details, ended_at, started_at, type)
	         VALUES (?, ?, ?, ?)`

	var details any
	if strings.TrimSpace(event.Details) == "" {
		details = nil // Inserts NULL
	} else {
		details = event.Details
	}

	_, err := d.db.ExecContext(ctx, stmt, details, event.EndedAt, event.StartedAt, event.Type)
	if err != nil {
		return fmt.Errorf("error inserting event: %w", err)
	}

	return nil
}

type InsertFileItem struct {
	AWSVersionID   string
	LastModifiedAt int64
	Path           string
	SHA256         string
	Size           int64
}

func (d *Database) InsertFiles(ctx context.Context, files []InsertFileItem) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO files (path, aws_version_id, sha256, size, last_modified_at)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("error preparing statement: %w", err)
	}
	defer stmt.Close()

	for _, file := range files {
		if _, err := stmt.ExecContext(ctx, file.Path, file.AWSVersionID, file.SHA256, file.Size, file.LastModifiedAt); err != nil {
			tx.Rollback()
			return fmt.Errorf("error inserting file (%s): %w", file.Path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
}

func (d *Database) DoesFileExist(ctx context.Context, path string, size, lastModifiedAt int64) (bool, error) {
	var count int
	err := d.fileExistsStmt.QueryRowContext(ctx, path, size, lastModifiedAt).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("error checking if file exists: %w", err)
	}
	return count > 0, nil
}

func (d *Database) GetSHA256(ctx context.Context, path, awsVersionID string) (string, error) {
	var sha256 string
	err := d.sha256Stmt.QueryRowContext(ctx, path, awsVersionID).Scan(&sha256)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil // No rows found
		}
		return "", fmt.Errorf("error querying SHA-256: %w", err)
	}

	return sha256, nil
}

type GetBackupsResultItem struct {
	Details   sql.NullString
	EndedAt   int64
	StartedAt int64
}

func (d *Database) GetBackups(ctx context.Context) ([]GetBackupsResultItem, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT started_at, ended_at, details
		FROM events
		WHERE type = 'backup'
		ORDER BY started_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("error querying backups: %w", err)
	}
	defer rows.Close()

	var results []GetBackupsResultItem
	for rows.Next() {
		var item GetBackupsResultItem
		if err := rows.Scan(&item.StartedAt, &item.EndedAt, &item.Details); err != nil {
			return nil, fmt.Errorf("error scanning backup row: %w", err)
		}
		results = append(results, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over backup rows: %w", err)
	}

	return results, nil
}
