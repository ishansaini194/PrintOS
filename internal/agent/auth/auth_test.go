package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// provisionServer returns a server that hands out a fixed identity and counts
// how many times it was called.
func provisionServer(calls *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		json.NewEncoder(w).Encode(map[string]string{
			"shop_id": "shop-abc",
			"token":   "secret-token-xyz",
		})
	}))
}

func TestEnsureTokenProvisionsThenReuses(t *testing.T) {
	var calls int32
	srv := provisionServer(&calls)
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "printos-token")

	// First run: no file yet → provisions and writes the token file.
	shopID, token, err := EnsureToken(srv.URL, "PRINT-CODE1", tokenPath)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if shopID != "shop-abc" || token != "secret-token-xyz" {
		t.Fatalf("got (%q, %q), want (shop-abc, secret-token-xyz)", shopID, token)
	}
	if calls != 1 {
		t.Fatalf("provision called %d times on first run, want 1", calls)
	}

	// Second run: file exists → must read it WITHOUT calling provision again.
	// Passing an empty setup code proves it isn't re-provisioning.
	shopID2, token2, err := EnsureToken(srv.URL, "", tokenPath)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if shopID2 != shopID || token2 != token {
		t.Fatalf("second run returned (%q, %q), want (%q, %q)", shopID2, token2, shopID, token)
	}
	if calls != 1 {
		t.Fatalf("provision called %d times total, want 1 (second run must reuse file)", calls)
	}
}

func TestEnsureTokenNotProvisioned(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "printos-token")
	// No file and no setup code → error.
	if _, _, err := EnsureToken("http://unused", "", tokenPath); err == nil {
		t.Fatal("expected error when not provisioned and no setup code")
	}
}
