// Package server holds the Fiber app and the HTTP start/port logic.
package server

import (
	"os"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// Server bundles the Fiber app with its shared dependencies.
type Server struct {
	App *fiber.App
	DB  *gorm.DB
}

// New creates a Server with a fresh Fiber app. BodyLimit is raised well above
// the 50MB upload cap (the authoritative size check lives in the upload
// handler / render) so large uploads reach the handler instead of being
// rejected by Fiber's 4MB default.
func New(db *gorm.DB) *Server {
	app := fiber.New(fiber.Config{
		BodyLimit: 64 << 20, // 64MB
	})
	return &Server{App: app, DB: db}
}

// Start reads PORT (default "8080") and listens.
func (s *Server) Start() error {
	port := env("PORT", "8080")
	return s.App.Listen(":" + port)
}

// env returns the environment variable or a fallback default.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
