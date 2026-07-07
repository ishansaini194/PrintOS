package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
)

// fakeJobs is an in-memory Jobs for testing, mirroring the store's semantics.
type fakeJobs struct {
	mu       sync.Mutex
	rows     map[string]store.Job
	payments map[string]store.Payment // keyed by job id
	refunds  map[string]int           // recorded refunds, keyed by payment id
}

func (f *fakeJobs) Create(p store.NewJob) (store.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = make(map[string]store.Job)
	}
	j := store.Job{
		ID:             fmt.Sprintf("job-%d", len(f.rows)+1),
		ShopID:         p.ShopID,
		IdempotencyKey: p.IdempotencyKey,
		Mode:           "release",
		State:          store.JobAwaitingPayment,
		ClaimCode:      p.ClaimCode,
		Type:           p.Type,
		Copies:         p.Copies,
		Pages:          p.Pages,
		AmountPaise:    p.AmountPaise,
		Duplex:         p.Duplex,
		PaperSize:      p.PaperSize,
		ExpiresAt:      p.ExpiresAt,
	}
	f.rows[j.ID] = j
	return j, nil
}

func (f *fakeJobs) Get(id string) (store.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.rows[id]
	if !ok {
		return store.Job{}, store.ErrNotFound
	}
	return j, nil
}

func (f *fakeJobs) SetState(id, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.rows[id]
	if !ok {
		return store.ErrNotFound
	}
	j.State = state
	f.rows[id] = j
	return nil
}

func (f *fakeJobs) MarkPaid(id, razorpayPaymentID string, expiresAt time.Time) (store.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.rows[id]
	if !ok || j.State != store.JobAwaitingPayment {
		return store.Job{}, store.ErrNotFound
	}
	j.State = store.JobPaid
	j.ExpiresAt = expiresAt
	f.rows[id] = j
	if p, ok := f.payments[id]; ok && p.Status == store.PaymentCreated {
		p.Status = store.PaymentPaid
		p.RazorpayPaymentID = razorpayPaymentID
		f.payments[id] = p
	}
	return j, nil
}

func (f *fakeJobs) PaymentByJob(jobID string) (store.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.payments[jobID]
	if !ok {
		return store.Payment{}, store.ErrPaymentNotFound
	}
	return p, nil
}

func (f *fakeJobs) SavePaymentOrder(jobID string, amountPaise int, razorpayOrderID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.payments == nil {
		f.payments = make(map[string]store.Payment)
	}
	p, ok := f.payments[jobID]
	if !ok {
		p = store.Payment{ID: "pmt-" + jobID, JobID: jobID, AmountPaise: amountPaise, Status: store.PaymentCreated}
	}
	if p.Status == store.PaymentCreated {
		p.RazorpayOrderID = razorpayOrderID
	}
	f.payments[jobID] = p
	return nil
}

func (f *fakeJobs) RefundablePayments() ([]store.RefundablePayment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.RefundablePayment
	for jobID, p := range f.payments {
		j, ok := f.rows[jobID]
		if !ok || p.Status != store.PaymentPaid {
			continue
		}
		if j.State == store.JobExpired || j.State == "failed" {
			out = append(out, store.RefundablePayment{
				PaymentID: p.ID, JobID: jobID, JobState: j.State,
				RazorpayPaymentID: p.RazorpayPaymentID, AmountPaise: p.AmountPaise,
			})
		}
	}
	return out, nil
}

func (f *fakeJobs) MarkRefunded(paymentID, reason, gatewayRefundID, status string, amountPaise int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for jobID, p := range f.payments {
		if p.ID == paymentID && p.Status == store.PaymentPaid {
			p.Status = store.PaymentRefunded
			f.payments[jobID] = p
			if f.refunds == nil {
				f.refunds = make(map[string]int)
			}
			f.refunds[paymentID]++
		}
	}
	return nil
}

func (f *fakeJobs) SetSHA(id, sha string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.rows[id]
	if !ok {
		return store.ErrNotFound
	}
	j.PDFSHA256 = sha
	f.rows[id] = j
	return nil
}

func (f *fakeJobs) MarkHeld(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j, ok := f.rows[id]; ok && j.State == store.JobPaid {
		j.State = store.JobHeld
		f.rows[id] = j
	}
	return nil
}

func (f *fakeJobs) FindReleasable(shopID, claimCode string) (store.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	for _, j := range f.rows {
		if j.ShopID == shopID && j.ClaimCode == claimCode &&
			(j.State == store.JobPaid || j.State == store.JobHeld) &&
			j.ExpiresAt.After(now) {
			return j, nil
		}
	}
	return store.Job{}, store.ErrNotFound
}

func (f *fakeJobs) ClaimCodeActive(shopID, claimCode string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	for _, j := range f.rows {
		if j.ShopID == shopID && j.ClaimCode == claimCode &&
			j.State != "done" && j.State != "failed" && j.State != store.JobExpired &&
			j.ExpiresAt.After(now) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeJobs) ExpireDue(now time.Time) ([]store.ExpiredJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var expired []store.ExpiredJob
	for id, j := range f.rows {
		if (j.State == store.JobPaid || j.State == store.JobHeld) && j.ExpiresAt.Before(now) {
			j.State = store.JobExpired
			f.rows[id] = j
			expired = append(expired, store.ExpiredJob{ID: j.ID, ShopID: j.ShopID})
		}
	}
	return expired, nil
}

func uploadApp(shops Shops, jobs Jobs) *fiber.App {
	app := fiber.New()
	h := NewHandlers(shops, jobs, &fakeGateway{})
	app.Post("/upload", h.Upload)
	return app
}

// multipartBody builds a multipart request body with the given form fields and,
// if fileField is non-empty, a file part.
func multipartBody(t *testing.T, fields map[string]string, fileField, fileName string, fileData []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	if fileField != "" {
		fw, err := w.CreateFormFile(fileField, fileName)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(fileData)
	}
	w.Close()
	return &buf, w.FormDataContentType()
}

func doUpload(t *testing.T, app *fiber.App, body *bytes.Buffer, contentType string) int {
	t.Helper()
	req := httptest.NewRequest("POST", "/upload", body)
	req.Header.Set("Content-Type", contentType)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestUploadMissingShop(t *testing.T) {
	app := uploadApp(&fakeShops{active: true}, &fakeJobs{})
	body, ct := multipartBody(t, map[string]string{}, "file", "a.pdf", []byte("%PDF-1.4"))
	if s := doUpload(t, app, body, ct); s != fiber.StatusBadRequest {
		t.Fatalf("missing shop_id: status = %d, want 400", s)
	}
}

func TestUploadMissingFile(t *testing.T) {
	app := uploadApp(&fakeShops{active: true}, &fakeJobs{})
	body, ct := multipartBody(t, map[string]string{"shop_id": "s1"}, "", "", nil)
	if s := doUpload(t, app, body, ct); s != fiber.StatusBadRequest {
		t.Fatalf("missing file: status = %d, want 400", s)
	}
}

func TestUploadInactiveShop(t *testing.T) {
	app := uploadApp(&fakeShops{active: false}, &fakeJobs{})
	body, ct := multipartBody(t, map[string]string{"shop_id": "s1"}, "file", "a.pdf", []byte("%PDF-1.4"))
	if s := doUpload(t, app, body, ct); s != fiber.StatusBadRequest {
		t.Fatalf("inactive shop: status = %d, want 400", s)
	}
}

// samplePDF returns a minimal but valid n-page PDF (correct xref offsets).
func samplePDF(t *testing.T, n int) []byte {
	t.Helper()
	objs := []string{"<< /Type /Catalog /Pages 2 0 R >>"}
	kids := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			kids += " "
		}
		kids += fmt.Sprintf("%d 0 R", 3+i)
	}
	objs = append(objs, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, n))
	for i := 0; i < n; i++ {
		objs = append(objs, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>")
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", len(objs)+1)
	b.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func TestUploadUnsupportedType(t *testing.T) {
	// A valid shop + a file with an unknown extension → render.Normalize rejects
	// it as unsupported → 400 (no gs/soffice needed for this path).
	app := uploadApp(&fakeShops{active: true}, &fakeJobs{})
	body, ct := multipartBody(t, map[string]string{"shop_id": "s1"}, "file", "drawing.xyz", []byte("garbage"))
	if s := doUpload(t, app, body, ct); s != fiber.StatusBadRequest {
		t.Fatalf("unsupported type: status = %d, want 400", s)
	}
}

// TestUploadAwaitsPayment runs the real upload path (needs gs + pdfinfo) and
// checks: correct amount (pages × copies × rate), a 6-digit numeric claim code,
// job in awaiting_payment, and NOTHING pushed to any agent.
func TestUploadAwaitsPayment(t *testing.T) {
	for _, tool := range []string{"gs", "pdfinfo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed; skipping", tool)
		}
	}
	t.Setenv("PRINTOS_PDF_DIR", t.TempDir())

	jobs := &fakeJobs{}
	app := uploadApp(&fakeShops{active: true}, jobs)

	// 3 pages × 2 copies × ₹10/page (color) = ₹60 = 6000 paise.
	fields := map[string]string{"shop_id": "s1", "type": "color", "copies": "2"}
	body, ct := multipartBody(t, fields, "file", "doc.pdf", samplePDF(t, 3))
	req := httptest.NewRequest("POST", "/upload", body)
	req.Header.Set("Content-Type", ct)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var out struct {
		JobID       string `json:"job_id"`
		AmountPaise int    `json:"amount_paise"`
		ClaimCode   string `json:"claim_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.AmountPaise != 6000 {
		t.Errorf("amount_paise = %d, want 6000 (3 pages × 2 copies × 1000)", out.AmountPaise)
	}
	if len(out.ClaimCode) != 6 {
		t.Errorf("claim_code = %q, want 6 digits", out.ClaimCode)
	}
	for _, r := range out.ClaimCode {
		if r < '0' || r > '9' {
			t.Errorf("claim_code %q is not numeric", out.ClaimCode)
		}
	}

	// The job awaits payment and was never pushed (no agent is connected; a push
	// attempt would have failed the request with a 202/error path).
	job, err := jobs.Get(out.JobID)
	if err != nil {
		t.Fatalf("job not stored: %v", err)
	}
	if job.State != store.JobAwaitingPayment {
		t.Errorf("state = %q, want awaiting_payment", job.State)
	}
	if job.Pages != 3 || job.Copies != 2 || job.Type != "color" {
		t.Errorf("job row = %+v, want pages=3 copies=2 type=color", job)
	}
	if job.PDFSHA256 == "" {
		t.Error("pdf sha not recorded on job row")
	}
}
