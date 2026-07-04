// Package queue is the agent's persistent, crash-safe job store (SQLite).
// Jobs are written to disk BEFORE printing so a crash never loses a paid job,
// and deduped on the idempotency key so a re-sent job never double-prints.
package queue

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// ErrDuplicate is returned by Enqueue when the idempotency key already exists.
var ErrDuplicate = errors.New("duplicate job (idempotency key seen)")

// Queue is a handle to the on-disk job store.
type Queue struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs migrations.
func Open(path string) (*Queue, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Queue{db: db}, nil
}

// Close closes the database.
func (q *Queue) Close() error {
	return q.db.Close()
}

// Enqueue writes a job to disk in the "printing"-ready state BEFORE any print
// attempt. If the idempotency key was already seen, it returns ErrDuplicate and
// does not insert — the caller must NOT print again.
func (q *Queue) Enqueue(job protocol.Job) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	now := time.Now().UTC()
	_, err = q.db.Exec(
		`INSERT INTO jobs (id, idempotency_key, state, payload, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		job.ID, job.IdempotencyKey, string(protocol.StatePrinting), string(payload), now, now,
	)
	if err != nil {
		// UNIQUE constraint on idempotency_key → duplicate.
		return ErrDuplicate
	}
	return nil
}

// SetState updates a job's state (printing → done / failed / uncertain).
func (q *Queue) SetState(id string, state protocol.JobState) error {
	res, err := q.db.Exec(
		`UPDATE jobs SET state = ?, updated_at = ? WHERE id = ?`,
		string(state), time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("set state: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job %s not found", id)
	}
	return nil
}

// Pending returns jobs still in the "printing" state — used on restart to
// resume or mark uncertain after a crash.
func (q *Queue) Pending() ([]protocol.Job, error) {
	rows, err := q.db.Query(
		`SELECT payload FROM jobs WHERE state = ?`,
		string(protocol.StatePrinting),
	)
	if err != nil {
		return nil, fmt.Errorf("query pending: %w", err)
	}
	defer rows.Close()

	var jobs []protocol.Job
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var j protocol.Job
		if err := json.Unmarshal([]byte(payload), &j); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
