package store

import (
	"errors"
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

// makeJob inserts a job with a unique idempotency key and the given claim code.
func makeJob(t *testing.T, js *JobStore, shopID, claimCode string) Job {
	t.Helper()
	job, err := js.Create(NewJob{
		ShopID:         shopID,
		IdempotencyKey: fmt.Sprintf("idem-%d", time.Now().UnixNano()),
		ClaimCode:      claimCode,
		Type:           "color",
		Copies:         2,
		Pages:          3,
		AmountPaise:    6000,
		PaperSize:      "A4",
		ExpiresAt:      time.Now().Add(time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return job
}

func TestJobCreateAndSetState(t *testing.T) {
	js := testDB(t)
	shopID := makeShop(t, js)

	job := makeJob(t, js, shopID, "123456")
	if job.ID == "" {
		t.Fatal("expected a generated job id")
	}
	if job.State != JobAwaitingPayment {
		t.Errorf("state = %q, want awaiting_payment", job.State)
	}
	if job.Mode != "release" {
		t.Errorf("mode = %q, want release", job.Mode)
	}
	if job.ClaimCode != "123456" || job.ShopID != shopID {
		t.Errorf("unexpected job row: %+v", job)
	}
	if job.Type != "color" || job.Copies != 2 || job.Pages != 3 || job.AmountPaise != 6000 {
		t.Errorf("pricing fields wrong: %+v", job)
	}

	if err := js.SetState(job.ID, "done"); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	got, err := js.Get(job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "done" {
		t.Errorf("state after SetState = %q, want done", got.State)
	}
}

func TestJobGetMissing(t *testing.T) {
	js := testDB(t)
	if _, err := js.Get("00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing: err = %v, want ErrNotFound", err)
	}
}

func TestFindReleasableGuards(t *testing.T) {
	js := testDB(t)
	shopID := makeShop(t, js)
	job := makeJob(t, js, shopID, "654321")

	// Unpaid → never releasable.
	if _, err := js.FindReleasable(shopID, job.ClaimCode); !errors.Is(err, ErrNotFound) {
		t.Errorf("unpaid job releasable: err = %v, want ErrNotFound", err)
	}

	// Paid → releasable.
	if err := js.SetState(job.ID, JobPaid); err != nil {
		t.Fatal(err)
	}
	got, err := js.FindReleasable(shopID, job.ClaimCode)
	if err != nil || got.ID != job.ID {
		t.Fatalf("paid job: got %+v err %v, want %s", got, err, job.ID)
	}

	// Held (agent acked) → still releasable.
	if err := js.MarkHeld(job.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := js.Get(job.ID); got.State != JobHeld {
		t.Fatalf("MarkHeld: state = %q, want held", got.State)
	}
	if _, err := js.FindReleasable(shopID, job.ClaimCode); err != nil {
		t.Errorf("held job: err = %v, want releasable", err)
	}

	// Done → no longer releasable; wrong code never matches.
	if err := js.SetState(job.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := js.FindReleasable(shopID, job.ClaimCode); !errors.Is(err, ErrNotFound) {
		t.Errorf("done job releasable: err = %v, want ErrNotFound", err)
	}
}

func TestMarkHeldOnlyFromPaid(t *testing.T) {
	js := testDB(t)
	shopID := makeShop(t, js)
	job := makeJob(t, js, shopID, "777777")

	// From awaiting_payment a (bogus) ack must not hold the job.
	if err := js.MarkHeld(job.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := js.Get(job.ID); got.State != JobAwaitingPayment {
		t.Errorf("state = %q, want awaiting_payment (MarkHeld must not fire)", got.State)
	}

	// From printing a late ack must not regress the job.
	if err := js.SetState(job.ID, "printing"); err != nil {
		t.Fatal(err)
	}
	if err := js.MarkHeld(job.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := js.Get(job.ID); got.State != "printing" {
		t.Errorf("state = %q, want printing (late ack must not regress)", got.State)
	}
}

func TestClaimCodeActive(t *testing.T) {
	js := testDB(t)
	shopID := makeShop(t, js)
	job := makeJob(t, js, shopID, "888888")

	active, err := js.ClaimCodeActive(shopID, job.ClaimCode)
	if err != nil || !active {
		t.Fatalf("active = %v err %v, want true for a live job", active, err)
	}
	if err := js.SetState(job.ID, "done"); err != nil {
		t.Fatal(err)
	}
	active, err = js.ClaimCodeActive(shopID, job.ClaimCode)
	if err != nil || active {
		t.Errorf("active = %v err %v, want false after done", active, err)
	}
}
