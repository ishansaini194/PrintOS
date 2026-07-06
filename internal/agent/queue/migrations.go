package queue

import (
	"database/sql"
	"fmt"
)

// migrations is the ordered list of schema steps. Append new steps; never edit
// or reorder existing ones. Each runs once, tracked in schema_version.
var migrations = []string{
	// 1: initial jobs table
	`CREATE TABLE jobs (
		id              TEXT PRIMARY KEY,
		idempotency_key TEXT NOT NULL UNIQUE,
		state           TEXT NOT NULL,
		payload         TEXT NOT NULL,
		created_at      TIMESTAMP NOT NULL,
		updated_at      TIMESTAMP NOT NULL
	)`,
	// 2: type column so workers can claim jobs by printer type (mono/color)
	// without deserializing the payload blob. Existing rows default to mono.
	`ALTER TABLE jobs ADD COLUMN type TEXT NOT NULL DEFAULT 'mono'`,
}

// migrate brings the database up to the latest schema version, applying only
// the steps not yet run. Safe to call on every Open.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`,
	); err != nil {
		return fmt.Errorf("ensure schema_version: %w", err)
	}

	var current int
	row := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("read version: %w", err)
	}

	for i := current; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}
	}
	return nil
}
