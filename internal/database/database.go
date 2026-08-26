package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// pragmaSQL configures connection-level settings, re-applied on every write connection.
const pragmaSQL = `
PRAGMA journal_mode=WAL;        -- Enables concurrent reads
PRAGMA foreign_keys=ON;         -- Enforce foreign key constraints
PRAGMA synchronous=FULL;        -- Maximum durability (slower but safer)
PRAGMA temp_store=MEMORY;       -- Store temp data in memory for performance
PRAGMA mmap_size=268435456;     -- 256MB memory mapping for better performance
PRAGMA cache_size=10000;        -- Larger cache for better performance
PRAGMA integrity_check;         -- Enable additional integrity checks
`

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

	// Pin to a single physical connection: walked_paths is a session-scoped TEMP TABLE,
	// so all queries against it must share the same underlying SQLite connection.
	db.SetMaxOpenConns(1)

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

	// Configure connection pragmas and apply pending schema migrations (only for write mode)
	if !readonly {
		if _, err := db.ExecContext(ctx, pragmaSQL); err != nil {
			db.Close()
			if lockFile != nil {
				lockFile.Close()
				os.Remove(lockFile.Name())
			}
			return nil, fmt.Errorf("error setting pragmas: %w", err)
		}

		if err := runMigrations(ctx, db); err != nil {
			db.Close()
			if lockFile != nil {
				lockFile.Close()
				os.Remove(lockFile.Name())
			}
			return nil, fmt.Errorf("error applying migrations: %w", err)
		}

		// walked_paths tracks which known files were seen during the current backup walk
		if _, err := db.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS walked_paths (path TEXT PRIMARY KEY)`); err != nil {
			db.Close()
			if lockFile != nil {
				lockFile.Close()
				os.Remove(lockFile.Name())
			}
			return nil, fmt.Errorf("error creating walked_paths temp table: %w", err)
		}
	}

	// Prepare commonly used statements
	fileExistsStmt, err := db.PrepareContext(ctx, `
        SELECT COUNT(path)
        FROM files
        WHERE path = ? AND size = ? AND last_modified_at = ? AND deleted_at IS NULL
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

// runMigrations applies any embedded migration files newer than the database's recorded version.
func runMigrations(ctx context.Context, db *sql.DB) error {
	// We need the schema_migrations table to exist before we can check the current version.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL DEFAULT (unixepoch())
		)
	`); err != nil {
		return fmt.Errorf("error creating schema_migrations table: %w", err)
	}

	var currentVersion int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentVersion); err != nil {
		return fmt.Errorf("error reading current schema version: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("error reading migrations directory: %w", err)
	}

	type migration struct {
		version int
		name    string
	}
	var migrations []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return fmt.Errorf("invalid migration filename %q: missing version prefix", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return fmt.Errorf("invalid migration filename %q: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{version: version, name: entry.Name()})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })

	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}

		contents, err := fs.ReadFile(migrationsFS, "migrations/"+m.name)
		if err != nil {
			return fmt.Errorf("error reading migration %q: %w", m.name, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("error beginning transaction for migration %q: %w", m.name, err)
		}

		if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
			tx.Rollback()
			return fmt.Errorf("error applying migration %q: %w", m.name, err)
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, m.version); err != nil {
			tx.Rollback()
			return fmt.Errorf("error recording migration %q: %w", m.name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("error committing migration %q: %w", m.name, err)
		}
	}

	return nil
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
	var walErr, stmtErr, dbErr, lockErr error

	if d.db != nil && !d.readonly {
		_, walErr = d.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
	}

	// Close prepared statements (important so driver can fully release file handles)
	if d.fileExistsStmt != nil {
		if err := d.fileExistsStmt.Close(); err != nil {
			stmtErr = fmt.Errorf("error closing fileExistsStmt: %w", err)
		}
	}
	if d.sha256Stmt != nil {
		if err := d.sha256Stmt.Close(); err != nil {
			if stmtErr == nil {
				stmtErr = fmt.Errorf("error closing sha256Stmt: %w", err)
			} else {
				stmtErr = fmt.Errorf("%v; error closing sha256Stmt: %w", stmtErr, err)
			}
		}
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
	if stmtErr != nil {
		return stmtErr
	}
	if dbErr != nil {
		return dbErr
	}
	return lockErr
}
