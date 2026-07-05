package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// adminApp builds a tiny app whose /admin/ping is gated by AdminAuth. The
// middleware captures the env key at creation, so set it before calling this.
func adminApp() *fiber.App {
	app := fiber.New()
	admin := app.Group("/admin", AdminAuth())
	admin.Get("/ping", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func adminStatus(t *testing.T, app *fiber.App, header string) int {
	t.Helper()
	req := httptest.NewRequest("GET", "/admin/ping", nil)
	if header != "" {
		req.Header.Set("X-Admin-Key", header)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestAdminAuthWithKey(t *testing.T) {
	t.Setenv("PRINTOS_ADMIN_KEY", "s3cr3t-long-key")
	app := adminApp()

	if s := adminStatus(t, app, "s3cr3t-long-key"); s != fiber.StatusOK {
		t.Errorf("correct key: status = %d, want 200", s)
	}
	if s := adminStatus(t, app, "wrong-key"); s != fiber.StatusUnauthorized {
		t.Errorf("wrong key: status = %d, want 401", s)
	}
	if s := adminStatus(t, app, ""); s != fiber.StatusUnauthorized {
		t.Errorf("missing key: status = %d, want 401", s)
	}
}

func TestAdminAuthUnsetKeyFailsClosed(t *testing.T) {
	t.Setenv("PRINTOS_ADMIN_KEY", "")
	app := adminApp()

	// Even a "correct-looking" header must be rejected when no key is configured.
	if s := adminStatus(t, app, "anything"); s != fiber.StatusServiceUnavailable {
		t.Errorf("unset key: status = %d, want 503", s)
	}
	if s := adminStatus(t, app, ""); s != fiber.StatusServiceUnavailable {
		t.Errorf("unset key, no header: status = %d, want 503", s)
	}
}
