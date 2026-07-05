package api

import (
	"crypto/subtle"
	"os"

	"github.com/gofiber/fiber/v2"
)

// AdminAuth gates operator-only routes behind the PRINTOS_ADMIN_KEY secret,
// supplied per request via the X-Admin-Key header. The expected key is captured
// once when the middleware is created.
//
// It fails closed: if PRINTOS_ADMIN_KEY is unset, every request is rejected with
// 503 so admin routes are never accidentally left open.
func AdminAuth() fiber.Handler {
	expected := os.Getenv("PRINTOS_ADMIN_KEY")

	return func(c *fiber.Ctx) error {
		if expected == "" {
			return c.Status(fiber.StatusServiceUnavailable).
				JSON(fiber.Map{"error": "admin key not configured"})
		}
		got := c.Get("X-Admin-Key")
		if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			return c.Status(fiber.StatusUnauthorized).
				JSON(fiber.Map{"error": "unauthorized"})
		}
		return c.Next()
	}
}
