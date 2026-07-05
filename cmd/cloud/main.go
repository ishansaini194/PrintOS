// Command cloud is the PrintOS cloud backend entry point. Keep this thin:
// load .env, wire the app, start the server.
package main

import (
	"log"

	"github.com/joho/godotenv"

	"github.com/ishansaini194/PrintOS/internal/cloud/app"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

func main() {
	// Load .env into the process environment (no-op if the file is absent).
	_ = godotenv.Load()

	srv, err := app.New()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("PrintOS cloud starting (protocol %s)", protocol.Version)
	if err := srv.Start(); err != nil {
		log.Fatal(err)
	}
}
