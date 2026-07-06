// Package conn is the agent's outbound pull connection to the cloud.
// The agent dials OUT and holds the connection; it never accepts inbound.
package conn

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// Handler processes an incoming envelope from the cloud.
type Handler func(protocol.Envelope) error

// Conn is a single logical connection to the cloud, with auto-reconnect.
type Conn struct {
	url       string
	handler   Handler
	onConnect func() // called after each successful (re)connect

	// mu guards ws and serializes writes. gorilla/websocket permits only one
	// concurrent writer, and Send is called from many goroutines (heartbeat plus
	// one per printer worker), so every write must hold mu.
	mu sync.Mutex
	ws *websocket.Conn
}

// New creates a Conn. url is the cloud WebSocket endpoint; handler is called
// for every envelope received.
func New(url string, handler Handler) *Conn {
	return &Conn{url: url, handler: handler}
}

// OnConnect sets a hook run right after each successful (re)connect — used to
// send the hello message identifying the shop.
func (c *Conn) OnConnect(fn func()) {
	c.onConnect = fn
}

// Run dials the cloud and reads until the connection drops, then reconnects
// with exponential backoff. Blocks until stop is closed.
func (c *Conn) Run(stop <-chan struct{}) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-stop:
			return
		default:
		}

		if err := c.dialAndRead(stop); err != nil {
			// wait, then retry with growing backoff
			select {
			case <-stop:
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second // reset after a clean session
	}
}

// dialAndRead opens one connection and reads envelopes until it closes.
func (c *Conn) dialAndRead(stop <-chan struct{}) error {
	ws, _, err := websocket.DefaultDialer.Dial(c.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	c.mu.Lock()
	c.ws = ws
	c.mu.Unlock()
	defer func() {
		ws.Close()
		c.mu.Lock()
		c.ws = nil
		c.mu.Unlock()
	}()

	// Unblock the blocking ReadMessage below when stop fires: closing the socket
	// makes ReadMessage return an error so the read loop exits promptly. done is
	// closed when dialAndRead returns, so this goroutine never leaks on reconnect.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			ws.Close()
		case <-done:
		}
	}()

	if c.onConnect != nil {
		c.onConnect() // send hello, etc.
	}

	for {
		select {
		case <-stop:
			return nil
		default:
		}

		_, data, err := ws.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue // skip malformed message, keep connection
		}
		if c.handler != nil {
			_ = c.handler(env)
		}
	}
}

// Send marshals and sends an envelope to the cloud (heartbeat, status, ack).
// Safe to call from multiple goroutines: mu serializes the single permitted
// websocket writer.
func (c *Conn) Send(env protocol.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ws == nil {
		return fmt.Errorf("not connected")
	}
	return c.ws.WriteMessage(websocket.TextMessage, data)
}
