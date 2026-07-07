package api

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
)

// fakeShops is an in-memory Shops for testing, with no database.
type fakeShops struct {
	consumed  bool   // whether Consume has already succeeded once
	lastHash  string // token hash passed to the last successful Consume
	tokenHash string // value returned by TokenHash
	active    bool   // value returned by IsActive
}

func (f *fakeShops) Create(name, setupCode string) (string, error) {
	return "shop-123", nil
}

func (f *fakeShops) Consume(setupCode, tokenHash string) (string, error) {
	if f.consumed {
		return "", store.ErrCodeUnusable // reused code
	}
	f.consumed = true
	f.lastHash = tokenHash
	return "shop-123", nil
}

func (f *fakeShops) TokenHash(shopID string) (string, error) {
	return f.tokenHash, nil
}

func (f *fakeShops) IsActive(shopID string) (bool, error) {
	return f.active, nil
}

func provisionApp(shops Shops) *fiber.App {
	app := fiber.New()
	h := NewHandlers(shops, nil, nil)
	app.Post("/agent/provision", h.Provision)
	return app
}

func postProvision(t *testing.T, app *fiber.App, code string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/agent/provision",
		strings.NewReader(`{"setup_code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

func TestProvisionValidCodeReturnsToken(t *testing.T) {
	shops := &fakeShops{}
	app := provisionApp(shops)

	status, out := postProvision(t, app, "PRINT-ABCDEF")
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("expected a non-empty token")
	}
	if out["shop_id"] != "shop-123" {
		t.Fatalf("shop_id = %v, want shop-123", out["shop_id"])
	}
	// The code must be marked used, and only the hash stored (never raw token).
	if !shops.consumed {
		t.Fatal("expected setup code to be consumed")
	}
	if shops.lastHash == token || shops.lastHash != sha256hex(token) {
		t.Fatal("Consume must receive the token hash, not the raw token")
	}
}

func TestProvisionReusedCodeUnauthorized(t *testing.T) {
	shops := &fakeShops{}
	app := provisionApp(shops)

	if status, _ := postProvision(t, app, "PRINT-ABCDEF"); status != fiber.StatusOK {
		t.Fatalf("first provision status = %d, want 200", status)
	}
	// Reusing the same (now consumed) code must be rejected.
	if status, _ := postProvision(t, app, "PRINT-ABCDEF"); status != fiber.StatusUnauthorized {
		t.Fatalf("reused provision status = %d, want 401", status)
	}
}

func TestProvisionMissingCodeBadRequest(t *testing.T) {
	app := provisionApp(&fakeShops{})
	if status, _ := postProvision(t, app, ""); status != fiber.StatusBadRequest {
		t.Fatalf("empty code status = %d, want 400", status)
	}
}
