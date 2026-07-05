// Package api holds the cloud's HTTP/WebSocket handlers.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// testPDFPath is the fixed PDF served at /jobs/:id/pdf until real storage exists.
const testPDFPath = "testdata/sample.pdf"

// registry maps shopID → connected agent socket, so jobs route to the right shop.
var registry = struct {
	mu    sync.Mutex
	conns map[string]*websocket.Conn
}{conns: make(map[string]*websocket.Conn)}

func register(shopID string, c *websocket.Conn) {
	registry.mu.Lock()
	registry.conns[shopID] = c
	registry.mu.Unlock()
}

func unregister(shopID string) {
	registry.mu.Lock()
	delete(registry.conns, shopID)
	registry.mu.Unlock()
}

// PushToAgent sends an envelope to a specific shop's agent.
func PushToAgent(shopID string, env protocol.Envelope) error {
	registry.mu.Lock()
	c := registry.conns[shopID]
	registry.mu.Unlock()
	if c == nil {
		return fmt.Errorf("shop %s not connected", shopID)
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.TextMessage, data)
}

// AgentSocket handles one connected agent's WebSocket.
func AgentSocket(c *websocket.Conn) {
	shopID := "" // set once the hello message arrives
	defer func() {
		if shopID != "" {
			log.Printf("agent disconnected: shop=%s", shopID)
			unregister(shopID)
		}
		c.Close()
	}()

	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}

		// First real message must be hello; register the shop.
		if env.Type == protocol.MsgHello {
			var m protocol.HelloMsg
			if json.Unmarshal(env.Payload, &m) == nil && m.ShopID != "" {
				shopID = m.ShopID
				register(shopID, c)
				log.Printf("agent connected: shop=%s", shopID)
			}
			continue
		}

		handleEnvelope(shopID, env)
	}
}

func handleEnvelope(shopID string, env protocol.Envelope) {
	switch env.Type {
	case protocol.MsgHeartbeat:
		var m protocol.HeartbeatMsg
		if json.Unmarshal(env.Payload, &m) == nil {
			log.Printf("[%s] heartbeat: v%s printer=%s queue=%d", shopID, m.AgentVersion, m.PrinterStatus, m.QueueDepth)
		}
	case protocol.MsgJobAck:
		var m protocol.JobAckMsg
		if json.Unmarshal(env.Payload, &m) == nil {
			log.Printf("[%s] ack: job=%s duplicate=%v", shopID, m.JobID, m.Duplicate)
		}
	case protocol.MsgStatus:
		var m protocol.StatusMsg
		if json.Unmarshal(env.Payload, &m) == nil {
			log.Printf("[%s] status: job=%s state=%s", shopID, m.JobID, m.State)
		}
	default:
		log.Printf("[%s] unknown message type: %s", shopID, env.Type)
	}
}

// TestPushJob builds a sample job and pushes it to the given shop's agent.
// If key is non-empty it's used as the idempotency key (pass the same key
// twice to test the duplicate guard).
func TestPushJob(shopID, key string) error {
	if shopID == "" {
		shopID = "test-shop"
	}
	if key == "" {
		key = "idem-" + time.Now().Format("150405.000")
	}
	jobID := "test-" + time.Now().Format("150405.000")
	job := protocol.Job{
		ID:             jobID,
		ShopID:         shopID,
		IdempotencyKey: key,
		Mode:           protocol.ModePrintNow,
		ClaimCode:      "A7",
		PDFURL:         publicURL() + "/jobs/" + jobID + "/pdf",
		PDFSHA256:      samplePDFSHA(),
		Settings:       protocol.PrintSettings{Color: protocol.ColorMono, Copies: 1, PaperSize: "A4"},
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().Add(2 * time.Hour).UTC(),
	}
	payload, _ := json.Marshal(protocol.JobPushMsg{Job: job})
	return PushToAgent(shopID, protocol.Envelope{
		Type:            protocol.MsgJobPush,
		ProtocolVersion: protocol.Version,
		SentAt:          time.Now().UTC(),
		Payload:         payload,
	})
}

// publicURL is the base URL the agent uses to reach the cloud for downloads.
func publicURL() string {
	if v := os.Getenv("PRINTOS_PUBLIC_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}

// samplePDFSHA returns the sha256 of the served test PDF so the agent's checksum
// check matches. Returns "" (skip checksum) if the file can't be read.
func samplePDFSHA() string {
	data, err := os.ReadFile(testPDFPath)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
