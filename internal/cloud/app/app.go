// Package app wires the cloud backend together: database, migrations, server,
// and routes. It is the single place where the pieces are assembled.
package app

import (
	"log"
	"os"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"

	"github.com/ishansaini194/PrintOS/internal/cloud/api"
	"github.com/ishansaini194/PrintOS/internal/cloud/server"
	"github.com/ishansaini194/PrintOS/internal/cloud/store"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// New connects the database, runs migrations, builds the server, and registers
// routes. It returns a ready-to-start Server.
func New() (*server.Server, error) {
	// 1. Connect to PostgreSQL.
	db, err := store.Connect()
	if err != nil {
		return nil, err
	}

	// 2. Run .sql migrations (explicit, data-safe).
	if err := store.RunMigrations(db, "migrations"); err != nil {
		return nil, err
	}
	log.Println("migrations applied")

	// 3. Build the server and register routes.
	srv := server.New(db)
	h := api.NewHandlers(store.NewShops(db), store.NewJobStore(db))
	registerRoutes(srv, h)
	return srv, nil
}

// registerRoutes mounts all HTTP/WebSocket endpoints on the server.
func registerRoutes(srv *server.Server, h *api.Handlers) {
	app := srv.App

	// CORS: the browser UI is served from a different origin than the cloud, so
	// it needs an explicit Access-Control-Allow-Origin. Kept to known origins
	// (configurable via PRINTOS_CORS_ORIGIN) rather than "*" because uploads and
	// later auth make a wildcard unsafe. OPTIONS is allowed for the preflight the
	// browser sends before a POST. Registered before routes so it covers them all.
	app.Use(cors.New(cors.Config{
		AllowOrigins: env("PRINTOS_CORS_ORIGIN", "http://localhost:5173"),
		AllowMethods: "GET,POST,OPTIONS",
		AllowHeaders: "Content-Type",
	}))

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":   "ok",
			"protocol": protocol.Version,
		})
	})

	// Admin (operator-only): gated behind the X-Admin-Key secret.
	admin := app.Group("/admin", api.AdminAuth())
	admin.Post("/shops", h.CreateShop) // create a shop, get its one-time setup code

	// Agent provisioning: exchange a setup code for a long-lived token.
	app.Post("/agent/provision", h.Provision)

	// Customer upload: file → clean PDF → job awaiting payment. Returns the
	// price and claim code; nothing reaches the agent until payment.
	app.Post("/upload", h.Upload)

	// Payment confirmation (STUB — real gateway later): marks the job paid and
	// pushes it to the shop's agent, which holds it.
	app.Post("/pay/confirm", h.PayConfirm)

	// Release: the claim code typed at the shop prints the held job.
	app.Post("/release", h.Release)

	// The shop PC keeps this page open to type claim codes into.
	app.Get("/shop/:shop_id/release", h.ReleasePage)

	// Agent pull-connection: only allow WebSocket upgrades here.
	app.Use("/agent", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	app.Get("/agent", websocket.New(h.AgentSocket))

	// Job PDF download: serve the stored <job_id>.pdf from local disk. 404 if
	// missing. The agent fetches this URL to print.
	app.Get("/jobs/:id/pdf", func(c *fiber.Ctx) error {
		path := api.PDFPath(c.Params("id"))
		if _, err := os.Stat(path); err != nil {
			return c.SendStatus(fiber.StatusNotFound)
		}
		return c.SendFile(path)
	})
}

// env returns the environment variable or a fallback default.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
