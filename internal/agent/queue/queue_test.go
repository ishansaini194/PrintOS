package queue

import (
	"context"
	"path/filepath"
	"sync"
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

func typedJob(id, key, typ string) protocol.Job {
	j := sampleJob(id, key)
	j.Type = typ
	return j
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

func TestGetNextFiltersByType(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()
	if err := q.Enqueue(typedJob("mono1", "km", "mono")); err != nil {
		t.Fatalf("enqueue mono: %v", err)
	}
	if err := q.Enqueue(typedJob("color1", "kc", "color")); err != nil {
		t.Fatalf("enqueue color: %v", err)
	}

	// Each type gets only its own job, never the other's.
	got, err := q.GetNext(ctx, "color")
	if err != nil || got == nil || got.ID != "color1" {
		t.Fatalf("GetNext(color): got %+v err %v, want color1", got, err)
	}
	got, err = q.GetNext(ctx, "mono")
	if err != nil || got == nil || got.ID != "mono1" {
		t.Fatalf("GetNext(mono): got %+v err %v, want mono1", got, err)
	}

	// Both claimed → nothing left for either type.
	if got, err := q.GetNext(ctx, "color"); err != nil || got != nil {
		t.Errorf("GetNext(color) after claim: got %+v err %v, want nil", got, err)
	}
	if got, err := q.GetNext(ctx, "mono"); err != nil || got != nil {
		t.Errorf("GetNext(mono) after claim: got %+v err %v, want nil", got, err)
	}
}

func TestGetNextEmptyTypeDefaultsMono(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()
	// A job with no Type must be claimable as "mono" (backward-compat default).
	if err := q.Enqueue(sampleJob("j1", "k1")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got, err := q.GetNext(ctx, "color"); err != nil || got != nil {
		t.Fatalf("GetNext(color): got %+v err %v, want nil", got, err)
	}
	got, err := q.GetNext(ctx, "mono")
	if err != nil || got == nil || got.ID != "j1" {
		t.Fatalf("GetNext(mono): got %+v err %v, want j1", got, err)
	}
}

func TestGetNextFIFO(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if err := q.Enqueue(typedJob(id, id, "mono")); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}
	for _, want := range []string{"a", "b", "c"} {
		got, err := q.GetNext(ctx, "mono")
		if err != nil || got == nil || got.ID != want {
			t.Fatalf("GetNext: got %+v err %v, want %s", got, err, want)
		}
	}
}

// TestGetNextRaceSafe verifies two workers of the same type never claim the same
// job. Run with -race to also catch data races.
func TestGetNextRaceSafe(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	const n = 50
	for i := 0; i < n; i++ {
		id := "job-" + itoa(i)
		if err := q.Enqueue(typedJob(id, id, "color")); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}

	const workers = 4
	var mu sync.Mutex
	seen := make(map[string]int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, err := q.GetNext(ctx, "color")
				if err != nil {
					t.Errorf("GetNext: %v", err)
					return
				}
				if job == nil {
					return // queue drained
				}
				mu.Lock()
				seen[job.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Errorf("claimed %d distinct jobs, want %d", len(seen), n)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("job %s claimed %d times, want exactly 1", id, count)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
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
