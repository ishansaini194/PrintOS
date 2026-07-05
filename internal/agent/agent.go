// Package agent ties the pieces together: it holds the pull connection, the
// persistent queue, the printer, the heartbeat and the updater, and drives the
// job lifecycle (receive → persist → print → report).
package agent

import (
	"encoding/json"
	"time"

	"github.com/ishansaini194/PrintOS/internal/agent/conn"
	"github.com/ishansaini194/PrintOS/internal/agent/download"
	"github.com/ishansaini194/PrintOS/internal/agent/health"
	"github.com/ishansaini194/PrintOS/internal/agent/printer"
	"github.com/ishansaini194/PrintOS/internal/agent/queue"
	"github.com/ishansaini194/PrintOS/internal/agent/updater"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// Config holds everything the agent needs to start.
type Config struct {
	CloudWSURL   string        // pull-connection endpoint
	UpdateURL    string        // updater check endpoint
	ShopID       string        // this shop's identifier (sent on connect)
	PrinterName  string        // target printer
	Version      string        // this build's version
	HeartbeatInt time.Duration // heartbeat interval
	UpdateInt    time.Duration // update-check interval
}

// Agent is the running coordinator.
type Agent struct {
	cfg     Config
	queue   *queue.Queue
	conn    *conn.Conn
	printer *printer.Printer
}

// New builds an Agent from its parts.
func New(cfg Config, q *queue.Queue, p *printer.Printer) *Agent {
	a := &Agent{cfg: cfg, queue: q, printer: p}
	a.conn = conn.New(cfg.CloudWSURL, a.handle)
	a.conn.OnConnect(a.sendHello)
	return a
}

// sendHello identifies this shop to the cloud right after connecting.
func (a *Agent) sendHello() {
	payload, _ := json.Marshal(protocol.HelloMsg{ShopID: a.cfg.ShopID})
	_ = a.conn.Send(a.envelope(protocol.MsgHello, payload))
}

// Run starts the connection, heartbeat and updater, and blocks until stop.
func (a *Agent) Run(stop <-chan struct{}) {
	hb := health.New(a.conn, a.cfg.HeartbeatInt, a.cfg.Version,
		func() protocol.PrinterStatus { return protocol.PrinterReady }, // TODO real status
		a.queueDepth,
	)
	up := updater.New(a.cfg.UpdateURL, a.cfg.Version, a.cfg.UpdateInt)

	go hb.Run(stop)
	go up.Run(stop, func() { /* TODO trigger restart */ })

	a.conn.Run(stop) // blocks until stop
}

// handle dispatches an incoming envelope from the cloud.
func (a *Agent) handle(env protocol.Envelope) error {
	if env.Type == protocol.MsgJobPush {
		var msg protocol.JobPushMsg
		if err := json.Unmarshal(env.Payload, &msg); err != nil {
			return err
		}
		a.processJob(msg.Job)
	}
	return nil
}

// processJob runs the job lifecycle: persist → ack → print → report status.
func (a *Agent) processJob(job protocol.Job) {
	// 1. Persist BEFORE printing. Duplicate key → ack, do not reprint.
	err := a.queue.Enqueue(job)
	dup := err == queue.ErrDuplicate
	a.sendAck(job, dup)
	if dup {
		return
	}

	// 2. Download the PDF to a local temp file (verifying its checksum) before
	// handing it to the printer.
	path, cleanup, err := download.ToTempFile(job.PDFURL, job.PDFSHA256)
	if err != nil {
		_ = a.queue.SetState(job.ID, protocol.StateFailed)
		a.sendStatus(job.ID, protocol.StateFailed)
		return
	}
	defer cleanup()

	// 3. Print the downloaded local file.
	state, _ := a.printer.Print(path, a.cfg.PrinterName)

	// 4. Record and report the outcome.
	_ = a.queue.SetState(job.ID, state)
	a.sendStatus(job.ID, state)
}

func (a *Agent) sendAck(job protocol.Job, dup bool) {
	payload, _ := json.Marshal(protocol.JobAckMsg{
		JobID:          job.ID,
		IdempotencyKey: job.IdempotencyKey,
		Duplicate:      dup,
	})
	_ = a.conn.Send(a.envelope(protocol.MsgJobAck, payload))
}

func (a *Agent) sendStatus(jobID string, state protocol.JobState) {
	payload, _ := json.Marshal(protocol.StatusMsg{
		JobID: jobID,
		State: state,
		At:    time.Now().UTC(),
	})
	_ = a.conn.Send(a.envelope(protocol.MsgStatus, payload))
}

func (a *Agent) envelope(t protocol.MessageType, payload json.RawMessage) protocol.Envelope {
	return protocol.Envelope{
		Type:            t,
		ProtocolVersion: protocol.Version,
		SentAt:          time.Now().UTC(),
		Payload:         payload,
	}
}

// queueDepth reports how many jobs are still printing (for the heartbeat).
func (a *Agent) queueDepth() int {
	pending, err := a.queue.Pending()
	if err != nil {
		return 0
	}
	return len(pending)
}
