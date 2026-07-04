// Package health sends periodic heartbeats to the cloud so it knows the agent
// is alive and what the printer's status is.
package health

import (
	"encoding/json"
	"time"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// Sender sends an envelope to the cloud (satisfied by conn.Conn).
type Sender interface {
	Send(protocol.Envelope) error
}

// StatusFunc reports the current printer status when called.
type StatusFunc func() protocol.PrinterStatus

// DepthFunc reports the current queue depth when called.
type DepthFunc func() int

// Heartbeat periodically pushes a HeartbeatMsg up to the cloud.
type Heartbeat struct {
	sender   Sender
	interval time.Duration
	version  string
	status   StatusFunc
	depth    DepthFunc
}

// New builds a Heartbeat. interval is typically 30–60s.
func New(sender Sender, interval time.Duration, version string, status StatusFunc, depth DepthFunc) *Heartbeat {
	return &Heartbeat{
		sender:   sender,
		interval: interval,
		version:  version,
		status:   status,
		depth:    depth,
	}
}

// Run ticks every interval, sending a heartbeat, until stop is closed.
func (h *Heartbeat) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			h.send()
		}
	}
}

// send builds one heartbeat envelope and sends it. Errors are ignored — the
// next tick will try again, and a missed heartbeat is how the cloud detects
// a down shop.
func (h *Heartbeat) send() {
	msg := protocol.HeartbeatMsg{
		AgentVersion:  h.version,
		PrinterStatus: h.status(),
		QueueDepth:    h.depth(),
		At:            time.Now().UTC(),
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_ = h.sender.Send(protocol.Envelope{
		Type:            protocol.MsgHeartbeat,
		ProtocolVersion: protocol.Version,
		SentAt:          time.Now().UTC(),
		Payload:         payload,
	})
}
