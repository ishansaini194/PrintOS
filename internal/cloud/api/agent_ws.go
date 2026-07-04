// Package api holds the cloud's HTTP/WebSocket handlers.
package api

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// hub tracks the currently connected agent socket so the cloud can push jobs.
// (v1: single agent for testing. A shop registry comes later.)
var hub = struct {
	mu   sync.Mutex
	conn *websocket.Conn
}{}

func setAgent(c *websocket.Conn) {
	hub.mu.Lock()
	hub.conn = c
	hub.mu.Unlock()
}

func clearAgent() {
	hub.mu.Lock()
	hub.conn = nil
	hub.mu.Unlock()
}

// PushToAgent sends an envelope down the connected agent socket.
func PushToAgent(env protocol.Envelope) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.conn == nil {
		return errNoAgent
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return hub.conn.WriteMessage(websocket.TextMessage, data)
}

var errNoAgent = &noAgentErr{}

type noAgentErr struct{}

func (e *noAgentErr) Error() string { return "no agent connected" }

// AgentSocket handles one connected agent's WebSocket.
func AgentSocket(c *websocket.Conn) {
	log.Println("agent connected")
	setAgent(c)
	defer func() {
		log.Println("agent disconnected")
		clearAgent()
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
		handleEnvelope(env)
	}
}

func handleEnvelope(env protocol.Envelope) {
	switch env.Type {
	case protocol.MsgHeartbeat:
		var m protocol.HeartbeatMsg
		if json.Unmarshal(env.Payload, &m) == nil {
			log.Printf("heartbeat: v%s printer=%s queue=%d", m.AgentVersion, m.PrinterStatus, m.QueueDepth)
		}
	case protocol.MsgJobAck:
		var m protocol.JobAckMsg
		if json.Unmarshal(env.Payload, &m) == nil {
			log.Printf("ack: job=%s duplicate=%v", m.JobID, m.Duplicate)
		}
	case protocol.MsgStatus:
		var m protocol.StatusMsg
		if json.Unmarshal(env.Payload, &m) == nil {
			log.Printf("status: job=%s state=%s", m.JobID, m.State)
		}
	default:
		log.Printf("unknown message type: %s", env.Type)
	}
}

// TestPushJob builds a sample job and pushes it to the connected agent.
// For wiring tests only — real job creation comes from payment later.
func TestPushJob() error {
	job := protocol.Job{
		ID:             "test-" + time.Now().Format("150405"),
		ShopID:         "test-shop",
		IdempotencyKey: "idem-" + time.Now().Format("150405.000"),
		Mode:           protocol.ModePrintNow,
		ClaimCode:      "A7",
		PDFURL:         "test.pdf",
		Settings:       protocol.PrintSettings{Color: protocol.ColorMono, Copies: 1, PaperSize: "A4"},
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().Add(2 * time.Hour).UTC(),
	}
	payload, _ := json.Marshal(protocol.JobPushMsg{Job: job})
	return PushToAgent(protocol.Envelope{
		Type:            protocol.MsgJobPush,
		ProtocolVersion: protocol.Version,
		SentAt:          time.Now().UTC(),
		Payload:         payload,
	})
}
