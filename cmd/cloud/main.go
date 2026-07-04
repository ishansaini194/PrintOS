// Command cloud is the PrintOS cloud backend entry point. Keep this thin:
// connect to Postgres, run migrations, start the HTTP server.
package main

import (
	"log"
	"os"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"

	"github.com/ishansaini194/PrintOS/internal/cloud/api"
	"github.com/ishansaini194/PrintOS/internal/cloud/store"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

func main() {
	// Load .env into the process environment (no-op if the file is absent),
	// so os.Getenv below picks up local config.
	_ = godotenv.Load()

	// 1. Connect to PostgreSQL.
	db, err := store.Connect()
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}

	// 2. Run .sql migrations (explicit, data-safe).
	if err := store.RunMigrations(db, "migrations"); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("migrations applied")

	// 3. Start the Fiber HTTP server.
	app := fiber.New()

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

	// Test-only: push a sample job to the connected agent.
	app.Post("/test/job", func(c *fiber.Ctx) error {
		if err := api.TestPushJob(); err != nil {
			return c.Status(503).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"pushed": true})
	})

	port := env("PORT", "8080")
	log.Printf("PrintOS cloud starting on :%s (protocol %s)", port, protocol.Version)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
