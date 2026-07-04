package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func sha(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// testServer serves LatestInfo at /latest and the binary at /binary.
func testServer(t *testing.T, version string, binary []byte, checksum string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LatestInfo{
			Version: version,
			URL:     srv.URL + "/binary",
			SHA256:  checksum,
		})
	})
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) {
		w.Write(binary)
	})
	return srv
}

func TestNoUpdateWhenSameVersion(t *testing.T) {
	srv := testServer(t, "1.0.0", []byte("x"), sha([]byte("x")))
	defer srv.Close()

	u := New(srv.URL+"/latest", "1.0.0", time.Hour)
	applied, err := u.checkAndApply()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Error("should not update when versions match")
	}
}

func TestChecksumMismatchRefuses(t *testing.T) {
	bin := []byte("new-binary")
	srv := testServer(t, "1.0.1", bin, "deadbeef") // wrong checksum
	defer srv.Close()

	u := New(srv.URL+"/latest", "1.0.0", time.Hour)
	applied, err := u.checkAndApply()
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if applied {
		t.Error("must not apply on bad checksum")
	}
}

func TestFetchLatest(t *testing.T) {
	bin := []byte("new-binary")
	srv := testServer(t, "1.0.1", bin, sha(bin))
	defer srv.Close()

	u := New(srv.URL+"/latest", "1.0.0", time.Hour)
	info, err := u.fetchLatest()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if info.Version != "1.0.1" {
		t.Errorf("version = %s", info.Version)
	}
}

func TestChecksumOK(t *testing.T) {
	data := []byte("hello")
	if !checksumOK(data, sha(data)) {
		t.Error("correct checksum should pass")
	}
	if checksumOK(data, "wrong") {
		t.Error("wrong checksum should fail")
	}
}
