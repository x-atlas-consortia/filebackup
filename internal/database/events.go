package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

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
