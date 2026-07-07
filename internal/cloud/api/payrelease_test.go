package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/razorpay"
	"github.com/ishansaini194/PrintOS/internal/cloud/store"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

const testKeySecret = "test-secret"

// fakeGateway implements Gateway in memory. Signature verification delegates
// to the real razorpay client so handler tests exercise the actual HMAC path.
type fakeGateway struct {
	mu        sync.Mutex
	orders    int      // orders created
	refunded  []string // razorpay payment ids refunded, in call order
	refundErr error    // when set, RefundPayment fails
}

func (g *fakeGateway) KeyID() string { return "rzp_test_key" }

func (g *fakeGateway) CreateOrder(amountPaise int, receipt string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.orders++
	return fmt.Sprintf("order_fake%d", g.orders), nil
}

func (g *fakeGateway) RefundPayment(razorpayPaymentID string, amountPaise int) (string, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refundErr != nil {
		return "", "", g.refundErr
	}
	g.refunded = append(g.refunded, razorpayPaymentID)
	return fmt.Sprintf("rfnd_fake%d", len(g.refunded)), "processed", nil
}

func (g *fakeGateway) VerifySignature(orderID, paymentID, signature string) bool {
	return razorpay.New("rzp_test_key", testKeySecret, "").VerifySignature(orderID, paymentID, signature)
}

func (g *fakeGateway) refundCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.refunded)
}

// sign produces a valid checkout signature for the test secret.
func sign(orderID, paymentID string) string {
	mac := hmac.New(sha256.New, []byte(testKeySecret))
	mac.Write([]byte(orderID + "|" + paymentID))
	return hex.EncodeToString(mac.Sum(nil))
}

func payReleaseApp(jobs Jobs) *fiber.App {
	app := fiber.New()
	h := NewHandlers(&fakeShops{active: true}, jobs, &fakeGateway{})
	app.Post("/pay/order", h.PayOrder)
	app.Post("/pay/verify", h.PayVerify)
	app.Post("/release", h.Release)
	app.Get("/shop/:shop_id/release", h.ReleasePage)
	return app
}

// orderFor drives /pay/order and returns the razorpay order id for the job.
func orderFor(t *testing.T, app *fiber.App, jobID string) string {
	t.Helper()
	status, out := postJSON(t, app, "/pay/order", `{"job_id":"`+jobID+`"}`)
	if status != fiber.StatusOK {
		t.Fatalf("/pay/order status = %d, want 200", status)
	}
	orderID, _ := out["razorpay_order_id"].(string)
	if orderID == "" {
		t.Fatal("/pay/order returned no razorpay_order_id")
	}
	return orderID
}

// verifyBody builds a /pay/verify request body with a signature over the pair.
func verifyBody(jobID, orderID, paymentID, signature string) string {
	b, _ := json.Marshal(map[string]string{
		"job_id":              jobID,
		"razorpay_order_id":   orderID,
		"razorpay_payment_id": paymentID,
		"razorpay_signature":  signature,
	})
	return string(b)
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

func TestPayOrderCreatesAndReusesOrder(t *testing.T) {
	jobs := &fakeJobs{}
	gw := &fakeGateway{}
	app := fiber.New()
	h := NewHandlers(&fakeShops{active: true}, jobs, gw)
	app.Post("/pay/order", h.PayOrder)
	j := seedJob(t, jobs, "101010", store.JobAwaitingPayment)

	status, out := postJSON(t, app, "/pay/order", `{"job_id":"`+j.ID+`"}`)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if out["razorpay_order_id"] != "order_fake1" || out["key_id"] != "rzp_test_key" {
		t.Errorf("response = %v, want order_fake1 + key id", out)
	}
	if amt, _ := out["amount_paise"].(float64); int(amt) != j.AmountPaise {
		t.Errorf("amount_paise = %v, want %d", out["amount_paise"], j.AmountPaise)
	}

	// Re-opening checkout reuses the pending order — no second gateway order.
	status, out = postJSON(t, app, "/pay/order", `{"job_id":"`+j.ID+`"}`)
	if status != fiber.StatusOK || out["razorpay_order_id"] != "order_fake1" {
		t.Fatalf("second order = %d %v, want 200 with the same order id", status, out)
	}
	if gw.orders != 1 {
		t.Errorf("gateway orders = %d, want 1", gw.orders)
	}
}

func TestPayOrderGuards(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	if status, _ := postJSON(t, app, "/pay/order", `{"job_id":"nope"}`); status != fiber.StatusNotFound {
		t.Fatalf("unknown job: status = %d, want 404", status)
	}
	j := seedJob(t, jobs, "202020", "done")
	if status, _ := postJSON(t, app, "/pay/order", `{"job_id":"`+j.ID+`"}`); status != fiber.StatusConflict {
		t.Fatalf("done job: status = %d, want 409", status)
	}
}

func TestPayVerifyMarksPaid(t *testing.T) {
	t.Setenv("PRINTOS_HOLD_TTL", "30m")
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	j := seedJob(t, jobs, "111111", store.JobAwaitingPayment)
	orderID := orderFor(t, app, j.ID)

	// No agent connected → push fails → 202 with the job left 'paid' for retry.
	before := time.Now().UTC()
	status, out := postJSON(t, app, "/pay/verify",
		verifyBody(j.ID, orderID, "pay_1", sign(orderID, "pay_1")))
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
	// The hold TTL starts at verification.
	if got.ExpiresAt.Before(before.Add(29*time.Minute)) || got.ExpiresAt.After(before.Add(31*time.Minute)) {
		t.Errorf("expires_at = %s, want about 30m from verify", got.ExpiresAt)
	}
	// The payment row carries the verified razorpay payment id.
	p, err := jobs.PaymentByJob(j.ID)
	if err != nil || p.Status != store.PaymentPaid || p.RazorpayPaymentID != "pay_1" {
		t.Errorf("payment = %+v (%v), want paid with pay_1", p, err)
	}
}

func TestPayVerifyRejectsTamperedSignature(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	j := seedJob(t, jobs, "131313", store.JobAwaitingPayment)
	orderID := orderFor(t, app, j.ID)

	// Signature over a different payment id — a browser lying about paying.
	status, _ := postJSON(t, app, "/pay/verify",
		verifyBody(j.ID, orderID, "pay_evil", sign(orderID, "pay_other")))
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for bad signature", status)
	}
	got, _ := jobs.Get(j.ID)
	if got.State != store.JobAwaitingPayment {
		t.Errorf("state = %q, job must stay unpaid on invalid signature", got.State)
	}
}

func TestPayVerifyRejectsForeignOrder(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	j := seedJob(t, jobs, "141414", store.JobAwaitingPayment)
	orderFor(t, app, j.ID)

	// A validly-signed pair for an order that was never created for this job.
	status, _ := postJSON(t, app, "/pay/verify",
		verifyBody(j.ID, "order_other", "pay_1", sign("order_other", "pay_1")))
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a foreign order", status)
	}
	got, _ := jobs.Get(j.ID)
	if got.State != store.JobAwaitingPayment {
		t.Errorf("state = %q, job must stay unpaid", got.State)
	}
}

func TestPayVerifyIdempotentOnceHeld(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	j := seedJob(t, jobs, "151515", store.JobAwaitingPayment)
	orderID := orderFor(t, app, j.ID)

	body := verifyBody(j.ID, orderID, "pay_1", sign(orderID, "pay_1"))
	if status, _ := postJSON(t, app, "/pay/verify", body); status != fiber.StatusAccepted {
		t.Fatalf("first verify status = %d, want 202 (shop offline)", status)
	}
	// Agent acks → held. A repeat verify must not push again: with no agent
	// connected a push attempt would 202, so a 200 proves no push happened.
	if err := jobs.MarkHeld(j.ID); err != nil {
		t.Fatal(err)
	}
	status, out := postJSON(t, app, "/pay/verify", body)
	if status != fiber.StatusOK {
		t.Fatalf("repeat verify status = %d, want 200 (no re-push)", status)
	}
	if out["state"] != store.JobHeld {
		t.Errorf("state = %v, want held", out["state"])
	}
}

func TestPayVerifyUnknownJob(t *testing.T) {
	app := payReleaseApp(&fakeJobs{})
	status, _ := postJSON(t, app, "/pay/verify",
		verifyBody("nope", "order_x", "pay_x", sign("order_x", "pay_x")))
	if status != fiber.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
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

func TestReleaseExpiredJobRefused(t *testing.T) {
	jobs := &fakeJobs{}
	app := payReleaseApp(jobs)
	j := seedJob(t, jobs, "565656", store.JobHeld)
	j.ExpiresAt = time.Now().Add(-time.Minute).UTC()
	jobs.rows[j.ID] = j

	status, _ := postJSON(t, app, "/release", `{"shop_id":"s1","code":"`+j.ClaimCode+`"}`)
	if status != fiber.StatusNotFound {
		t.Fatalf("status = %d, want 404 for expired job", status)
	}
}

// seedPaidPayment attaches a captured payment to a job, as if it had gone
// through order + verify.
func seedPaidPayment(t *testing.T, jobs *fakeJobs, jobID, rzpPaymentID string, amount int) {
	t.Helper()
	if err := jobs.SavePaymentOrder(jobID, amount, "order_"+jobID); err != nil {
		t.Fatal(err)
	}
	jobs.mu.Lock()
	p := jobs.payments[jobID]
	p.Status = store.PaymentPaid
	p.RazorpayPaymentID = rzpPaymentID
	jobs.payments[jobID] = p
	jobs.mu.Unlock()
}

func TestSweepRefundsExpiredJobOnce(t *testing.T) {
	jobs := &fakeJobs{}
	gw := &fakeGateway{}
	j := seedJob(t, jobs, "575757", store.JobHeld)
	seedPaidPayment(t, jobs, j.ID, "pay_rzp1", j.AmountPaise)
	j.ExpiresAt = time.Now().Add(-time.Minute).UTC()
	jobs.rows[j.ID] = j

	if err := SweepExpiredJobs(jobs, time.Now().UTC()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got, _ := jobs.Get(j.ID)
	if got.State != store.JobExpired {
		t.Fatalf("state = %q, want expired", got.State)
	}

	if err := SweepRefunds(jobs, gw); err != nil {
		t.Fatalf("refund sweep: %v", err)
	}
	if gw.refundCount() != 1 || gw.refunded[0] != "pay_rzp1" {
		t.Fatalf("gateway refunds = %v, want one for pay_rzp1", gw.refunded)
	}
	p, _ := jobs.PaymentByJob(j.ID)
	if p.Status != store.PaymentRefunded {
		t.Fatalf("payment status = %q, want refunded", p.Status)
	}

	// Sweeping again must not refund the same payment twice.
	if err := SweepRefunds(jobs, gw); err != nil {
		t.Fatalf("second refund sweep: %v", err)
	}
	if gw.refundCount() != 1 {
		t.Fatalf("gateway refunds after second sweep = %d, want 1", gw.refundCount())
	}
}

func TestSweepRefundsFailedJob(t *testing.T) {
	jobs := &fakeJobs{}
	gw := &fakeGateway{}
	j := seedJob(t, jobs, "676767", store.JobHeld)
	seedPaidPayment(t, jobs, j.ID, "pay_rzp2", j.AmountPaise)
	if err := jobs.SetState(j.ID, "failed"); err != nil {
		t.Fatal(err)
	}

	if err := SweepRefunds(jobs, gw); err != nil {
		t.Fatalf("refund sweep: %v", err)
	}
	if gw.refundCount() != 1 || gw.refunded[0] != "pay_rzp2" {
		t.Fatalf("gateway refunds = %v, want one for pay_rzp2", gw.refunded)
	}
	if jobs.refunds["pmt-"+j.ID] != 1 {
		t.Fatalf("recorded refunds = %d, want 1", jobs.refunds["pmt-"+j.ID])
	}
}

func TestSweepRefundsRetriesAfterGatewayError(t *testing.T) {
	jobs := &fakeJobs{}
	gw := &fakeGateway{refundErr: errors.New("gateway down")}
	j := seedJob(t, jobs, "686868", store.JobHeld)
	seedPaidPayment(t, jobs, j.ID, "pay_rzp3", j.AmountPaise)
	_ = jobs.SetState(j.ID, store.JobExpired)

	// Gateway failure: no refund recorded, payment must NOT be marked refunded.
	if err := SweepRefunds(jobs, gw); err != nil {
		t.Fatalf("refund sweep: %v", err)
	}
	if p, _ := jobs.PaymentByJob(j.ID); p.Status != store.PaymentPaid {
		t.Fatalf("payment status = %q, must stay paid when the gateway fails", p.Status)
	}

	// Gateway recovers → the next sweep issues the refund.
	gw.refundErr = nil
	if err := SweepRefunds(jobs, gw); err != nil {
		t.Fatalf("retry sweep: %v", err)
	}
	if gw.refundCount() != 1 || gw.refunded[0] != "pay_rzp3" {
		t.Fatalf("gateway refunds = %v, want one for pay_rzp3 after retry", gw.refunded)
	}
	if p, _ := jobs.PaymentByJob(j.ID); p.Status != store.PaymentRefunded {
		t.Fatalf("payment status = %q, want refunded after retry", p.Status)
	}
}

func TestSweepExpiredJobsSendsCancel(t *testing.T) {
	jobs := &fakeJobs{}
	j := seedJob(t, jobs, "585858", store.JobHeld)
	j.ExpiresAt = time.Now().Add(-time.Minute).UTC()
	jobs.rows[j.ID] = j

	var sentShop string
	var sent protocol.Envelope
	oldSend := sendToAgent
	sendToAgent = func(shopID string, env protocol.Envelope) error {
		sentShop = shopID
		sent = env
		return nil
	}
	defer func() { sendToAgent = oldSend }()

	if err := SweepExpiredJobs(jobs, time.Now().UTC()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if sentShop != j.ShopID || sent.Type != protocol.MsgCancel {
		t.Fatalf("sent shop/type = %q/%q, want %q/cancel", sentShop, sent.Type, j.ShopID)
	}
	var msg protocol.CancelMsg
	if err := json.Unmarshal(sent.Payload, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.JobID != j.ID {
		t.Fatalf("cancel job_id = %q, want %q", msg.JobID, j.ID)
	}
}

func TestSweepExpiredJobsIgnoresOfflineAgent(t *testing.T) {
	jobs := &fakeJobs{}
	j := seedJob(t, jobs, "595959", store.JobHeld)
	j.ExpiresAt = time.Now().Add(-time.Minute).UTC()
	jobs.rows[j.ID] = j

	oldSend := sendToAgent
	sendToAgent = func(shopID string, env protocol.Envelope) error {
		return errors.New("offline")
	}
	defer func() { sendToAgent = oldSend }()

	if err := SweepExpiredJobs(jobs, time.Now().UTC()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got, _ := jobs.Get(j.ID)
	if got.State != store.JobExpired {
		t.Fatalf("state = %q, want expired despite the offline agent", got.State)
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
