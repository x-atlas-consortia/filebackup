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

// New initializes a new Database instance
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
		readonly:       readonly,
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
	readonly       bool
	sha256Stmt     *sql.Stmt
}

// Reindex performs a REINDEX operation on the database
func (d *Database) Reindex(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `REINDEX`)
	if err != nil {
		return fmt.Errorf("error reindexing database: %w", err)
	}
	return nil
}

// Close closes the database connection and releases the lock
func (d *Database) Close(ctx context.Context) error {
	var walErr, dbErr, lockErr error

	if d.db != nil && !d.readonly {
		_, walErr = d.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
	}

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
	if walErr != nil {
		return walErr
	}
	if dbErr != nil {
		return dbErr
	}
	return lockErr
}

// InsertEvent represents an event to be inserted into the database
type InsertEvent struct {
	Details   string
	EndedAt   int64
	StartedAt int64
	Type      string
}

// InsertEvent inserts a new event into the database
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

// InsertFileItem represents a file to be inserted into the database
type InsertFileItem struct {
	AWSVersionID   string
	LastModifiedAt int64
	Path           string
	SHA256         string
	Size           int64
}

// InsertFiles inserts multiple files into the database in a single transaction
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

// DoesFileExist checks if a file with the given path, size, and last modified timestamp exists in the database
func (d *Database) DoesFileExist(ctx context.Context, path string, size, lastModifiedAt int64) (bool, error) {
	var count int
	err := d.fileExistsStmt.QueryRowContext(ctx, path, size, lastModifiedAt).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("error checking if file exists: %w", err)
	}
	return count > 0, nil
}

// GetSHA256 retrieves the SHA-256 hash for a given file path and AWS version ID
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

// GetEventsResultItem represents a single event retrieved from the database
type GetEventsResultItem struct {
	Details   sql.NullString
	EndedAt   int64
	StartedAt int64
}

// GetEvents retrieves events of a specific type from the database
func (d *Database) GetEvents(ctx context.Context, eventType string) ([]GetEventsResultItem, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT started_at, ended_at, details
		FROM events
		WHERE type = ?
		ORDER BY started_at DESC
	`, eventType)
	if err != nil {
		return nil, fmt.Errorf("error querying backups: %w", err)
	}
	defer rows.Close()

	var results []GetEventsResultItem
	for rows.Next() {
		var item GetEventsResultItem
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

// GetFilesResultItem represents a single file retrieved from the database
type GetFilesResultItem struct {
	LastModifiedAt int64
	Path           string
	Size           int64
	VersionID      string
}

// GetFiles retrieves the latest versions of files under a given path prefix up to a specified time
func (d *Database) GetFiles(ctx context.Context, pathPrefix string, time int64) ([]GetFilesResultItem, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT path, size, last_modified_at, aws_version_id
		FROM files
		WHERE (path, last_modified_at) IN (
			SELECT path, MAX(last_modified_at)
			FROM files
			WHERE path LIKE ? AND last_modified_at <= ?
			GROUP BY path
		);
	`, pathPrefix+"%", time)
	if err != nil {
		return nil, fmt.Errorf("error querying files: %w", err)
	}
	defer rows.Close()

	var files []GetFilesResultItem
	for rows.Next() {
		var file GetFilesResultItem
		if err := rows.Scan(&file.Path, &file.Size, &file.LastModifiedAt, &file.VersionID); err != nil {
			return nil, fmt.Errorf("error scanning file row: %w", err)
		}
		files = append(files, file)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over file rows: %w", err)
	}

	return files, nil
}

// GetVersionsResultItem represents a single version of a file
type GetVersionsResultItem struct {
	LastModifiedAt int64
	SHA256         string
	Size           int64
}

// GetVersions retrieves all versions of a file identified by its path
func (d *Database) GetVersions(ctx context.Context, filePath string) ([]GetVersionsResultItem, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT last_modified_at, size, sha256
		FROM files
		WHERE path = ?
		ORDER BY last_modified_at DESC
	`, filePath)
	if err != nil {
		return nil, fmt.Errorf("error querying versions: %w", err)
	}
	defer rows.Close()

	var versions []GetVersionsResultItem
	for rows.Next() {
		var version GetVersionsResultItem
		if err := rows.Scan(&version.LastModifiedAt, &version.Size, &version.SHA256); err != nil {
			return nil, fmt.Errorf("error scanning version row: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over version rows: %w", err)
	}

	return versions, nil
}
