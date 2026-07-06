// Package api holds the cloud's HTTP/WebSocket handlers.
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/gofiber/contrib/websocket"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

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
func (h *Handlers) AgentSocket(c *websocket.Conn) {
	shopID := "" // set once a valid hello message arrives
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

		// First real message must be a hello with a valid token. Anything else
		// (bad token, unknown shop) is rejected by closing the socket.
		if env.Type == protocol.MsgHello {
			var m protocol.HelloMsg
			if json.Unmarshal(env.Payload, &m) != nil {
				continue
			}
			if !h.verifyToken(m.ShopID, m.Token) {
				log.Printf("agent rejected: shop=%s (bad or missing token)", m.ShopID)
				return
			}
			shopID = m.ShopID
			register(shopID, c)
			log.Printf("agent connected: shop=%s", shopID)
			continue
		}

		h.handleEnvelope(shopID, env)
	}
}

func (h *Handlers) handleEnvelope(shopID string, env protocol.Envelope) {
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
			// The agent has the job persisted and is holding it. MarkHeld only
			// moves paid → held, so a late ack never regresses a printing/done job.
			if err := h.jobs.MarkHeld(m.JobID); err != nil {
				log.Printf("[%s] mark held: %v", shopID, err)
			}
		}
	case protocol.MsgStatus:
		var m protocol.StatusMsg
		if json.Unmarshal(env.Payload, &m) == nil {
			log.Printf("[%s] status: job=%s state=%s", shopID, m.JobID, m.State)
			if err := h.jobs.SetState(m.JobID, string(m.State)); err != nil {
				log.Printf("[%s] persist job state: %v", shopID, err)
			}
		}
	default:
		log.Printf("[%s] unknown message type: %s", shopID, env.Type)
	}
}

// publicURL is the base URL the agent uses to reach the cloud for downloads.
func publicURL() string {
	if v := os.Getenv("PRINTOS_PUBLIC_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}
