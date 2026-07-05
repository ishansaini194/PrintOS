package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
)

// Shops is the subset of store operations the auth handlers, hello verification,
// and upload need. Keeping it an interface lets the handlers be unit-tested with
// a fake, without a live database.
type Shops interface {
	Create(name, setupCode string) (shopID string, err error)
	Consume(setupCode, tokenHash string) (shopID string, err error)
	TokenHash(shopID string) (string, error)
	IsActive(shopID string) (bool, error)
}

// Jobs is the subset of job-store operations the upload handler and status
// updates need.
type Jobs interface {
	Create(shopID, idempotencyKey, claimCode string, expires time.Time) (store.Job, error)
	SetState(id, state string) error
}

// Handlers bundles the DB-backed HTTP/WebSocket handlers.
type Handlers struct {
	shops Shops
	jobs  Jobs
}

// NewHandlers builds the handler set over the shop and job stores.
func NewHandlers(shops Shops, jobs Jobs) *Handlers {
	return &Handlers{shops: shops, jobs: jobs}
}

// CreateShop provisions a new shop and returns its one-time setup code.
// TODO: this admin route is operator-only and currently unauthenticated.
func (h *Handlers) CreateShop(c *fiber.Ctx) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.BodyParser(&body); err != nil || body.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name required"})
	}

	code, err := genSetupCode()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	shopID, err := h.shops.Create(body.Name, code)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"shop_id": shopID, "setup_code": code})
}

// Provision exchanges a one-time setup code for a long-lived token. The raw
// token is returned exactly once; only its hash is stored.
func (h *Handlers) Provision(c *fiber.Ctx) error {
	var body struct {
		SetupCode string `json:"setup_code"`
	}
	if err := c.BodyParser(&body); err != nil || body.SetupCode == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "setup_code required"})
	}

	token, err := genToken()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	shopID, err := h.shops.Consume(body.SetupCode, sha256hex(token))
	if errors.Is(err, store.ErrCodeUnusable) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid or used setup code"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"shop_id": shopID, "token": token})
}

// verifyToken reports whether token matches the shop's stored hash.
func (h *Handlers) verifyToken(shopID, token string) bool {
	if shopID == "" || token == "" {
		return false
	}
	hash, err := h.shops.TokenHash(shopID)
	if err != nil || hash == "" {
		return false
	}
	return sha256hex(token) == hash
}

// setupCodeAlphabet excludes visually ambiguous characters (0/O, 1/I).
const setupCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// genSetupCode returns a short, human-typeable one-time code like "PRINT-7X9KQ2".
func genSetupCode() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = setupCodeAlphabet[int(b[i])%len(setupCodeAlphabet)]
	}
	return "PRINT-" + string(b), nil
}

// genToken returns a long random secret (32 bytes hex).
func genToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sha256hex returns the hex-encoded sha256 of s.
func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
