// Command agent is the PrintOS local agent entry point. Keep this thin:
// load config, wire the parts, run, and stop cleanly.
//
// On a shop PC the agent runs as a background service (see the install/start
// subcommands below); with no subcommand it runs interactively so `go run`
// still works for local development. The kardianos/service wrapper only wraps
// the existing start/stop — the agent's core logic is unchanged.
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"github.com/kardianos/service"

	"github.com/ishansaini194/PrintOS/internal/agent"
	"github.com/ishansaini194/PrintOS/internal/agent/auth"
	"github.com/ishansaini194/PrintOS/internal/agent/printer"
	"github.com/ishansaini194/PrintOS/internal/agent/printerinfo"
	"github.com/ishansaini194/PrintOS/internal/agent/queue"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

var svcConfig = &service.Config{
	Name:        "PrintOSAgent",
	DisplayName: "PrintOS Agent",
	Description: "PrintOS local print agent.",
	// Restart the service if it crashes, so a shop PC recovers unattended.
	// These keys are honored by the Windows service manager; harmless elsewhere.
	Option: service.KeyValue{
		"OnFailure":              "restart",
		"OnFailureDelayDuration": "5s",
		"OnFailureResetPeriod":   10,
		"DelayedAutoStart":       true,
	},
}

// program wraps the agent's start/stop for kardianos. Start returns quickly and
// the real work runs in run(); Stop closes the stop channel to trigger the
// existing clean-shutdown path and waits for run() to unwind.
type program struct {
	stop chan struct{}
	done chan struct{}
}

func (p *program) Start(s service.Service) error {
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	close(p.stop)
	// Give run() a bounded window to unwind (close conn, stop workers, close
	// the queue) before the service manager reaps the process.
	select {
	case <-p.done:
	case <-time.After(15 * time.Second):
		log.Println("shutdown timed out; exiting")
	}
	return nil
}

// run holds the existing agent startup: load env, load printers, provision,
// open the queue, then block in a.Run until Stop closes p.stop.
func (p *program) run() {
	defer close(p.done)

	// Load .env into the process environment (no-op if the file is absent).
	_ = godotenv.Load()

	// Detect + tag printer info on first run; later runs load printers.json silently.
	printerList, err := printerinfo.LoadOrTag(env("PRINTOS_PRINTERS_FILE", "printers.json"))
	if err != nil {
		log.Fatalf("printers: %v", err)
	}
	log.Printf("loaded %d printers: %v", len(printerList), printerList)

	// Provision on first run (setup code → token), then reuse the saved token.
	shopID, token, err := auth.EnsureToken(
		env("PRINTOS_PROVISION_URL", "http://localhost:8080/agent/provision"),
		os.Getenv("PRINTOS_SETUP_CODE"),
		env("PRINTOS_TOKEN_FILE", "printos-token"),
	)
	if err != nil {
		log.Fatalf("provision: %v", err)
	}

	cfg := agent.Config{
		CloudWSURL:   env("PRINTOS_CLOUD_WS", "ws://localhost:8080/agent"),
		UpdateURL:    env("PRINTOS_UPDATE_URL", "http://localhost:8080/agent/latest"),
		ShopID:       shopID,
		Token:        token,
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
	pr := printer.New(env("PRINTOS_SUMATRA", "SumatraPDF.exe"))
	a := agent.New(cfg, q, pr, printerList)

	log.Printf("PrintOS agent starting (protocol %s)", protocol.Version)
	a.Run(p.stop) // blocks until p.stop closes
	log.Println("stopped")
}

func main() {
	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatalf("service: %v", err)
	}

	// In service mode there is no terminal, so route logs to a file; keep the
	// console when running interactively (dev / `go run`).
	configureLogging()

	// A subcommand controls the OS service (install/uninstall/start/stop/...);
	// no subcommand runs the agent (interactively or under the service manager).
	if len(os.Args) > 1 {
		if err := service.Control(s, os.Args[1]); err != nil {
			log.Fatalf("%s: %v (valid actions: %v)", os.Args[1], err, service.ControlAction)
		}
		return
	}

	if err := s.Run(); err != nil {
		log.Fatalf("run: %v", err)
	}
}

// configureLogging sends logs to a file when running as a service (no terminal)
// and leaves them on the console when interactive. The file path comes from
// PRINTOS_LOG_FILE, defaulting to printos-agent.log next to the binary.
func configureLogging() {
	if service.Interactive() {
		return // keep console logging for dev
	}
	f, err := os.OpenFile(logFilePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		// Fall back to the default (stderr) rather than losing the process.
		log.Printf("log file: %v; logging to stderr", err)
		return
	}
	log.SetOutput(f)
}

// logFilePath is PRINTOS_LOG_FILE, or printos-agent.log beside the binary, or
// the working directory if the binary path can't be resolved.
func logFilePath() string {
	if v := os.Getenv("PRINTOS_LOG_FILE"); v != "" {
		return v
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "printos-agent.log")
	}
	return "printos-agent.log"
}

// env returns the environment variable or a fallback default.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
