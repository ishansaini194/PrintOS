package store

import (
	"fmt"
	"testing"
	"time"
)

// testDB connects to Postgres and applies migrations, or skips if unavailable.
func testDB(t *testing.T) *JobStore {
	t.Helper()
	db, err := Connect()
	if err != nil {
		t.Skipf("no database available: %v", err)
	}
	if err := RunMigrations(db, "../../../migrations"); err != nil {
		t.Skipf("migrations failed: %v", err)
	}
	return NewJobStore(db)
}

// makeShop inserts a throwaway shop and returns its id (jobs reference shops).
func makeShop(t *testing.T, js *JobStore) string {
	t.Helper()
	var id string
	if err := js.db.Raw(
		`INSERT INTO shops (name) VALUES ('jobs-test') RETURNING id`,
	).Scan(&id).Error; err != nil {
		t.Fatalf("insert shop: %v", err)
	}
	return id
}

func TestJobCreateAndSetState(t *testing.T) {
	js := testDB(t)
	shopID := makeShop(t, js)

	expires := time.Now().Add(time.Hour).UTC()
	idemKey := fmt.Sprintf("idem-%d", time.Now().UnixNano()) // unique per run
	job, err := js.Create(shopID, idemKey, "ABC123", expires)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if job.ID == "" {
		t.Fatal("expected a generated job id")
	}
	if job.State != "created" {
		t.Errorf("state = %q, want created", job.State)
	}
	if job.ClaimCode != "ABC123" || job.ShopID != shopID {
		t.Errorf("unexpected job row: %+v", job)
	}

	if err := js.SetState(job.ID, "done"); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	var state string
	if err := js.db.Raw(`SELECT state FROM jobs WHERE id = ?`, job.ID).Scan(&state).Error; err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state != "done" {
		t.Errorf("state after SetState = %q, want done", state)
	}
}
