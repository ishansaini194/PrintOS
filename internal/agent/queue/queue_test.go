package queue

import (
	"path/filepath"
	"testing"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	q, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { q.Close() })
	return q
}

func sampleJob(id, key string) protocol.Job {
	return protocol.Job{
		ID:             id,
		IdempotencyKey: key,
		Mode:           protocol.ModePrintNow,
		Settings:       protocol.PrintSettings{Color: protocol.ColorMono, Copies: 1, PaperSize: "A4"},
	}
}

func TestEnqueueAndPending(t *testing.T) {
	q := newTestQueue(t)
	if err := q.Enqueue(sampleJob("j1", "k1")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	pending, err := q.Pending()
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "j1" {
		t.Errorf("expected 1 pending job j1, got %+v", pending)
	}
}

func TestEnqueueDuplicate(t *testing.T) {
	q := newTestQueue(t)
	if err := q.Enqueue(sampleJob("j1", "k1")); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	// Same idempotency key → duplicate, must not insert again.
	err := q.Enqueue(sampleJob("j2", "k1"))
	if err != ErrDuplicate {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestSetState(t *testing.T) {
	q := newTestQueue(t)
	q.Enqueue(sampleJob("j1", "k1"))
	if err := q.SetState("j1", protocol.StateDone); err != nil {
		t.Fatalf("set state: %v", err)
	}
	// No longer in printing → Pending empty.
	pending, _ := q.Pending()
	if len(pending) != 0 {
		t.Errorf("expected no pending after done, got %d", len(pending))
	}
}

func TestSetStateMissing(t *testing.T) {
	q := newTestQueue(t)
	if err := q.SetState("nope", protocol.StateDone); err == nil {
		t.Error("expected error for missing job")
	}
}

func TestMigrationsRunOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	q1, err := Open(path)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	q1.Enqueue(sampleJob("j1", "k1"))
	q1.Close()

	// Reopen: migrations must not re-run or wipe data.
	q2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer q2.Close()
	pending, _ := q2.Pending()
	if len(pending) != 1 {
		t.Errorf("expected job to survive reopen, got %d", len(pending))
	}
}
