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

// New creates a Server with a fresh Fiber app.
func New(db *gorm.DB) *Server {
	return &Server{App: fiber.New(), DB: db}
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
