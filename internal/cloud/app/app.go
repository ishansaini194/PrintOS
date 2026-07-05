// Package app wires the cloud backend together: database, migrations, server,
// and routes. It is the single place where the pieces are assembled.
package app

import (
	"log"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"

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
	registerRoutes(srv)
	return srv, nil
}

// registerRoutes mounts all HTTP/WebSocket endpoints on the server.
func registerRoutes(srv *server.Server) {
	app := srv.App

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":   "ok",
			"protocol": protocol.Version,
		})
	})

	// Agent pull-connection: only allow WebSocket upgrades here.
	app.Use("/agent", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	app.Get("/agent", websocket.New(api.AgentSocket))

	// Job PDF download. No storage yet: serve a fixed test PDF. The :id param
	// is kept so this becomes a real per-job lookup later without a shape change.
	app.Get("/jobs/:id/pdf", func(c *fiber.Ctx) error {
		return c.SendFile("testdata/sample.pdf")
	})

	// Test-only: push a sample job to the connected agent.
	app.Post("/test/job", func(c *fiber.Ctx) error {
		if err := api.TestPushJob(c.Query("shop"), c.Query("key")); err != nil {
			return c.Status(503).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"pushed": true})
	})
}
