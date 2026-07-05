package api

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
)

// fakeJobs is an in-memory Jobs for testing.
type fakeJobs struct {
	created bool
}

func (f *fakeJobs) Create(shopID, idempotencyKey, claimCode string, expires time.Time) (store.Job, error) {
	f.created = true
	return store.Job{ID: "job-1", ShopID: shopID, IdempotencyKey: idempotencyKey,
		ClaimCode: claimCode, State: "created", ExpiresAt: expires}, nil
}

func (f *fakeJobs) SetState(id, state string) error { return nil }

func uploadApp(shops Shops, jobs Jobs) *fiber.App {
	app := fiber.New()
	h := NewHandlers(shops, jobs)
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

func TestUploadUnsupportedType(t *testing.T) {
	// A valid shop + a file with an unknown extension → render.Normalize rejects
	// it as unsupported → 400 (no gs/soffice needed for this path).
	app := uploadApp(&fakeShops{active: true}, &fakeJobs{})
	body, ct := multipartBody(t, map[string]string{"shop_id": "s1"}, "file", "drawing.xyz", []byte("garbage"))
	if s := doUpload(t, app, body, ct); s != fiber.StatusBadRequest {
		t.Fatalf("unsupported type: status = %d, want 400", s)
	}
}
