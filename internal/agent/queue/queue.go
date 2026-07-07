// Package queue is the agent's persistent, crash-safe job store (SQLite).
// Jobs are written to disk BEFORE printing so a crash never loses a paid job,
// and deduped on the idempotency key so a re-sent job never double-prints.
package queue

import (
	"context"
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
	// Serialize all access onto a single connection. SQLite allows only one
	// writer anyway, and this makes the atomic job-claim in GetNext safe against
	// many workers racing without any SQLITE_BUSY handling.
	db.SetMaxOpenConns(1)
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

// Enqueue writes a job to disk BEFORE any print attempt, so a crash never loses
// a paid job. Release-mode jobs (v1 default) land as "held" — they wait, invisible
// to workers, until Release moves them to "queued". Explicit print_now jobs land
// directly as "queued" for a worker to claim via GetNext. If the idempotency key
// was already seen, it returns ErrDuplicate and does not insert — the caller must
// NOT print again.
//
// The job's type is normalized (empty → mono) at this persist boundary and
// stored in its own column so GetNext can filter on it without deserializing.
func (q *Queue) Enqueue(job protocol.Job) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	state := protocol.StateQueued
	if job.PrintMode() == protocol.ModeRelease {
		state = protocol.StateHeld
	}
	now := time.Now().UTC()
	_, err = q.db.Exec(
		`INSERT INTO jobs (id, idempotency_key, state, type, payload, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.IdempotencyKey, string(state), job.PrinterType(), string(payload), now, now,
	)
	if err != nil {
		// UNIQUE constraint on idempotency_key → duplicate.
		return ErrDuplicate
	}
	return nil
}

// Release moves a held job to "queued" so the normal per-printer worker picks it
// up. It returns true if a held job was released, false if no held job with that
// id exists (already released, already printed, or unknown) — releasing twice is
// harmless.
func (q *Queue) Release(jobID string) (bool, error) {
	return q.releaseAt(jobID, time.Now().UTC())
}

func (q *Queue) releaseAt(jobID string, now time.Time) (bool, error) {
	payload, err := q.heldPayload(jobID)
	if err != nil {
		return false, err
	}
	if payload == "" {
		return false, nil
	}
	var job protocol.Job
	if err := json.Unmarshal([]byte(payload), &job); err != nil {
		return false, fmt.Errorf("unmarshal held job %s: %w", jobID, err)
	}
	if !job.ExpiresAt.IsZero() && !job.ExpiresAt.After(now) {
		if _, err := q.CancelHeld(jobID); err != nil {
			return false, err
		}
		return false, nil
	}

	res, err := q.db.Exec(
		`UPDATE jobs SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		string(protocol.StateQueued), now, jobID, string(protocol.StateHeld),
	)
	if err != nil {
		return false, fmt.Errorf("release job %s: %w", jobID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (q *Queue) heldPayload(jobID string) (string, error) {
	var payload string
	err := q.db.QueryRow(
		`SELECT payload FROM jobs WHERE id = ? AND state = ?`,
		jobID, string(protocol.StateHeld),
	).Scan(&payload)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("load held job %s: %w", jobID, err)
	default:
		return payload, nil
	}
}

// CancelHeld marks a still-held job terminally expired. Unknown, already
// released, printing or completed jobs are left untouched and reported as no-op.
func (q *Queue) CancelHeld(jobID string) (bool, error) {
	res, err := q.db.Exec(
		`UPDATE jobs SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		string(protocol.StateExpired), time.Now().UTC(), jobID, string(protocol.StateHeld),
	)
	if err != nil {
		return false, fmt.Errorf("cancel held job %s: %w", jobID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetNext atomically claims the oldest queued job of the given type, moving it
// from "queued" to "printing" and returning it. It returns (nil, nil) when no
// job of that type is waiting.
//
// The claim is a single UPDATE...RETURNING guarded by SQLite's write lock, so
// two workers of the same type racing for work never receive the same job — the
// loser's subquery no longer sees the row once the winner marks it "printing".
func (q *Queue) GetNext(ctx context.Context, jobType string) (*protocol.Job, error) {
	row := q.db.QueryRowContext(ctx,
		`UPDATE jobs SET state = ?, updated_at = ?
		 WHERE id = (
		     SELECT id FROM jobs
		     WHERE state = ? AND type = ?
		     ORDER BY created_at, id
		     LIMIT 1
		 )
		 RETURNING payload`,
		string(protocol.StatePrinting), time.Now().UTC(),
		string(protocol.StateQueued), jobType,
	)
	var payload string
	switch err := row.Scan(&payload); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("claim next %s job: %w", jobType, err)
	}
	var j protocol.Job
	if err := json.Unmarshal([]byte(payload), &j); err != nil {
		return nil, fmt.Errorf("unmarshal claimed job: %w", err)
	}
	return &j, nil
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

// Pending returns jobs not yet in a terminal state — "held" (awaiting release),
// "queued" (waiting for a worker) or "printing" (claimed, in progress). Used for
// the heartbeat's queue depth and, on restart, to resume or mark uncertain after
// a crash. Held jobs survive restarts here but never auto-print: only GetNext
// hands jobs to workers, and it selects "queued" alone.
func (q *Queue) Pending() ([]protocol.Job, error) {
	rows, err := q.db.Query(
		`SELECT payload FROM jobs WHERE state IN (?, ?, ?)`,
		string(protocol.StateHeld), string(protocol.StateQueued), string(protocol.StatePrinting),
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
