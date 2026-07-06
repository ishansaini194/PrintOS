package api

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
)

func payReleaseApp(jobs Jobs) *fiber.App {
	app := fiber.New()
	h := NewHandlers(&fakeShops{active: true}, jobs)
	app.Post("/pay/confirm", h.PayConfirm)
	app.Post("/release", h.Release)
	app.Get("/shop/:shop_id/release", h.ReleasePage)
	return app
}

func postJSON(t *testing.T, app *fiber.App, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// seedJob inserts a job directly into the fake store.
func seedJob(t *testing.T, jobs *fakeJobs, claimCode, state string) store.Job {
	t.Helper()
	j, err := jobs.Create(store.NewJob{
		ShopID: "s1", IdempotencyKey: "k-" + claimCode, ClaimCode: claimCode,
		Type: "mono", Copies: 1, Pages: 1, AmountPaise: 200,
		PaperSize: "A4", ExpiresAt: time.Now().Add(time.Hour).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if state != store.JobAwaitingPayment {
		if err := jobs.SetState(j.ID, state); err != nil {
			t.Fatal(err)
		}
		j.State = state
	}
	return j
}

func TestPayConfirmMarksPaid(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	j := seedJob(t, jobs, "111111", store.JobAwaitingPayment)

	// No agent connected → push fails → 202 with the job left 'paid' for retry.
	status, out := postJSON(t, app, "/pay/confirm", `{"job_id":"`+j.ID+`"}`)
	if status != fiber.StatusAccepted {
		t.Fatalf("status = %d, want 202 (shop offline)", status)
	}
	if out["state"] != store.JobPaid {
		t.Errorf("state = %v, want paid", out["state"])
	}
	got, _ := jobs.Get(j.ID)
	if got.State != store.JobPaid {
		t.Errorf("stored state = %q, want paid", got.State)
	}
}

func TestPayConfirmUnknownJob(t *testing.T) {
	app := payReleaseApp(&fakeJobs{})
	if status, _ := postJSON(t, app, "/pay/confirm", `{"job_id":"nope"}`); status != fiber.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

func TestPayConfirmTerminalJobConflict(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	j := seedJob(t, jobs, "222222", "done")
	if status, _ := postJSON(t, app, "/pay/confirm", `{"job_id":"`+j.ID+`"}`); status != fiber.StatusConflict {
		t.Fatalf("status = %d, want 409 for a done job", status)
	}
}

func TestReleaseWrongCode(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	seedJob(t, jobs, "333333", store.JobHeld)

	status, out := postJSON(t, app, "/release", `{"shop_id":"s1","code":"999999"}`)
	if status != fiber.StatusNotFound {
		t.Fatalf("status = %d, want 404 for wrong code", status)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "no job found") {
		t.Errorf("error = %q, want a clear 'no job found' message", msg)
	}
}

func TestReleaseUnpaidJobRefused(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	j := seedJob(t, jobs, "444444", store.JobAwaitingPayment)

	// The right code, but the job was never paid → must NOT release.
	status, _ := postJSON(t, app, "/release", `{"shop_id":"s1","code":"`+j.ClaimCode+`"}`)
	if status != fiber.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unpaid job", status)
	}
}

func TestReleaseHeldJobAgentOffline(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	j := seedJob(t, jobs, "555555", store.JobHeld)

	// Job is releasable, but no agent socket is registered → 503, no print.
	status, _ := postJSON(t, app, "/release", `{"shop_id":"s1","code":"`+j.ClaimCode+`"}`)
	if status != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when shop agent offline", status)
	}
}

func TestReleasePageServesShop(t *testing.T) {
	app := payReleaseApp(&fakeJobs{})
	req := httptest.NewRequest("GET", "/shop/s1/release", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "s1") {
		t.Error("page does not carry the shop id")
	}
	if !strings.Contains(string(body), "claim code") {
		t.Error("page does not prompt for the claim code")
	}
}
