// Command agent is the PrintOS local agent entry point. Keep this thin:
// load config, wire the parts, run, and stop cleanly.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/ishansaini194/PrintOS/internal/agent"
	"github.com/ishansaini194/PrintOS/internal/agent/printer"
	"github.com/ishansaini194/PrintOS/internal/agent/queue"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

func main() {
	// Load .env into the process environment (no-op if the file is absent).
	_ = godotenv.Load()

	cfg := agent.Config{
		CloudWSURL:   env("PRINTOS_CLOUD_WS", "ws://localhost:8080/agent"),
		UpdateURL:    env("PRINTOS_UPDATE_URL", "http://localhost:8080/agent/latest"),
		PrinterName:  env("PRINTOS_PRINTER", ""),
		Version:      protocol.Version,
		HeartbeatInt: 45 * time.Second,
		UpdateInt:    6 * time.Hour,
	}

	// Open the persistent queue.
	q, err := queue.Open(env("PRINTOS_DB", "printos-agent.db"))
	if err != nil {
		log.Fatalf("open queue: %v", err)
	}
	defer q.Close()

	// Build the printer and the agent.
	p := printer.New(env("PRINTOS_SUMATRA", "SumatraPDF.exe"))
	a := agent.New(cfg, q, p)

	// Stop cleanly on Ctrl+C / termination.
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("shutting down...")
		close(stop)
	}()

	log.Printf("PrintOS agent starting (protocol %s)", protocol.Version)
	a.Run(stop)
	log.Println("stopped")
}

// env returns the environment variable or a fallback default.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
