package database

import (
	"context"
	"database/sql"
	"fmt"
)

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

// MarkFilesDeleted marks each given path as deleted by timestamping deleted_at on its latest row
func (d *Database) MarkFilesDeleted(ctx context.Context, paths []string, deletedAt int64) error {
	if len(paths) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE files SET deleted_at = ?
		WHERE rowid = (
			SELECT rowid FROM files WHERE path = ?
			ORDER BY last_modified_at DESC, rowid DESC LIMIT 1
		)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("error preparing statement: %w", err)
	}
	defer stmt.Close()

	for _, path := range paths {
		if _, err := stmt.ExecContext(ctx, deletedAt, path); err != nil {
			tx.Rollback()
			return fmt.Errorf("error marking file deleted (%s): %w", path, err)
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

// InsertWalkedPaths records paths seen during the current backup walk in the walked_paths temp table
func (d *Database) InsertWalkedPaths(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO walked_paths (path) VALUES (?)`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("error preparing statement: %w", err)
	}
	defer stmt.Close()

	for _, path := range paths {
		if _, err := stmt.ExecContext(ctx, path); err != nil {
			tx.Rollback()
			return fmt.Errorf("error inserting walked path (%s): %w", path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}

	return nil
}

// GetDeletedPaths returns paths under pathPrefix whose latest version is not yet marked
// deleted but weren't seen in the current walk (i.e. newly-detected deletions)
func (d *Database) GetDeletedPaths(ctx context.Context, pathPrefix string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT f.path
		FROM (
			SELECT path, deleted_at,
				ROW_NUMBER() OVER (PARTITION BY path ORDER BY last_modified_at DESC, rowid DESC) AS rn
			FROM files WHERE path LIKE ?
		) f
		LEFT JOIN walked_paths w ON f.path = w.path
		WHERE f.rn = 1 AND f.deleted_at IS NULL AND w.path IS NULL
	`, pathPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("error querying deleted paths: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("error scanning deleted path row: %w", err)
		}
		paths = append(paths, path)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over deleted path rows: %w", err)
	}

	return paths, nil
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
        FROM (
            SELECT path, size, last_modified_at, aws_version_id,
                ROW_NUMBER() OVER (PARTITION BY path ORDER BY last_modified_at DESC, rowid ASC) AS rn
            FROM files
            WHERE path LIKE ? AND last_modified_at <= ?
        )
        WHERE rn = 1;
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

func (d *Database) GetLatestRandomFiles(ctx context.Context, number int) ([]GetFilesResultItem, error) {
	rows, err := d.db.QueryContext(ctx, `
        SELECT path, size, last_modified_at, aws_version_id
        FROM (
            SELECT path, size, last_modified_at, aws_version_id,
                ROW_NUMBER() OVER (PARTITION BY path ORDER BY last_modified_at DESC, rowid ASC) AS rn
            FROM files
        )
        WHERE rn = 1
        ORDER BY RANDOM()
        LIMIT ?;
	`, number)
	if err != nil {
		return nil, fmt.Errorf("error querying random files: %w", err)
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
