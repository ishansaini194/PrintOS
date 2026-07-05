package download

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func sha(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func serve(body []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
}

func TestToTempFileMatches(t *testing.T) {
	body := []byte("%PDF-1.4 fake pdf bytes")
	srv := serve(body)
	defer srv.Close()

	path, cleanup, err := ToTempFile(srv.URL, sha(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("temp file contents = %q, want %q", got, body)
	}

	// cleanup must remove the temp file.
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp file still exists after cleanup: %v", err)
	}
}

func TestToTempFileChecksumMismatch(t *testing.T) {
	body := []byte("real bytes")
	srv := serve(body)
	defer srv.Close()

	path, cleanup, err := ToTempFile(srv.URL, sha([]byte("different bytes")))
	if err == nil {
		cleanup()
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if path != "" || cleanup != nil {
		t.Fatal("expected empty path and nil cleanup on error")
	}
}

func TestToTempFileEmptySHASkipsCheck(t *testing.T) {
	body := []byte("whatever")
	srv := serve(body)
	defer srv.Close()

	path, cleanup, err := ToTempFile(srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error with empty sha: %v", err)
	}
	cleanup()
	_ = path
}

func TestToTempFileNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, _, err := ToTempFile(srv.URL, ""); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}
