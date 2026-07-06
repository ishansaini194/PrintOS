// Package agent ties the pieces together: it holds the pull connection, the
// persistent queue, the printer, the heartbeat and the updater, and drives the
// job lifecycle (receive → persist → print → report).
package agent

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/ishansaini194/PrintOS/internal/agent/conn"
	"github.com/ishansaini194/PrintOS/internal/agent/download"
	"github.com/ishansaini194/PrintOS/internal/agent/health"
	"github.com/ishansaini194/PrintOS/internal/agent/printer"
	"github.com/ishansaini194/PrintOS/internal/agent/printerinfo"
	"github.com/ishansaini194/PrintOS/internal/agent/queue"
	"github.com/ishansaini194/PrintOS/internal/agent/updater"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// workerPoll is how long a worker waits before re-checking the queue when it
// finds no job of its type.
const workerPoll = 500 * time.Millisecond

// Config holds everything the agent needs to start.
type Config struct {
	CloudWSURL   string        // pull-connection endpoint
	UpdateURL    string        // updater check endpoint
	ShopID       string        // this shop's identifier (sent on connect)
	Token        string        // auth token proving this shop (sent on connect)
	Version      string        // this build's version
	HeartbeatInt time.Duration // heartbeat interval
	UpdateInt    time.Duration // update-check interval
}

// Agent is the running coordinator.
type Agent struct {
	cfg      Config
	queue    *queue.Queue
	conn     *conn.Conn
	printer  *printer.Printer
	printers []printerinfo.Printer // one worker is run per printer
}

// New builds an Agent from its parts. printers is the tagged printer list; the
// agent runs one worker goroutine per printer, each pulling only its own type.
func New(cfg Config, q *queue.Queue, p *printer.Printer, printers []printerinfo.Printer) *Agent {
	a := &Agent{cfg: cfg, queue: q, printer: p, printers: printers}
	a.conn = conn.New(cfg.CloudWSURL, a.handle)
	a.conn.OnConnect(a.sendHello)
	return a
}

// sendHello identifies this shop to the cloud right after connecting.
func (a *Agent) sendHello() {
	payload, _ := json.Marshal(protocol.HelloMsg{ShopID: a.cfg.ShopID, Token: a.cfg.Token})
	_ = a.conn.Send(a.envelope(protocol.MsgHello, payload))
}

// Run starts the connection, heartbeat, updater and one worker per printer, and
// blocks until stop. On stop it cancels every worker and waits for them to drain
// so shutdown leaves no goroutine mid-print.
func (a *Agent) Run(stop <-chan struct{}) {
	hb := health.New(a.conn, a.cfg.HeartbeatInt, a.cfg.Version,
		func() protocol.PrinterStatus { return protocol.PrinterReady }, // TODO real status
		a.queueDepth,
	)
	up := updater.New(a.cfg.UpdateURL, a.cfg.Version, a.cfg.UpdateInt)

	go hb.Run(stop)
	go up.Run(stop, func() { /* TODO trigger restart */ })

	// Derive a context cancelled when stop closes, then fan it out to one worker
	// per printer. Each worker pulls only jobs of its own type, so N printers of
	// the same type naturally load-balance and all can print concurrently.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()

	var wg sync.WaitGroup
	for _, p := range a.printers {
		wg.Add(1)
		go func(p printerinfo.Printer) {
			defer wg.Done()
			a.worker(ctx, p)
		}(p)
	}

	a.conn.Run(stop) // blocks until stop
	cancel()         // belt-and-suspenders: unblock workers even if stop is already handled
	wg.Wait()        // no leaked worker goroutines past shutdown
}

// worker is one printer's loop: claim the next job of this printer's type, print
// it on THIS printer, repeat. It exits promptly when ctx is cancelled.
func (a *Agent) worker(ctx context.Context, p printerinfo.Printer) {
	for {
		job, err := a.queue.GetNext(ctx, p.Type)
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down; GetNext was cancelled
			}
			log.Printf("worker %s: claim job: %v", p.Name, err)
			if !sleepOrDone(ctx, workerPoll) {
				return
			}
			continue
		}
		if job == nil {
			if !sleepOrDone(ctx, workerPoll) { // nothing waiting for this type
				return
			}
			continue
		}
		a.printJob(*job, p.Name)
	}
}

// sleepOrDone waits for d or until ctx is cancelled. It returns false if ctx was
// cancelled (caller should stop), true if the delay elapsed.
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// handle dispatches an incoming envelope from the cloud.
func (a *Agent) handle(env protocol.Envelope) error {
	switch env.Type {
	case protocol.MsgJobPush:
		var msg protocol.JobPushMsg
		if err := json.Unmarshal(env.Payload, &msg); err != nil {
			return err
		}
		a.persistJob(msg.Job)
	case protocol.MsgRelease:
		var msg protocol.ReleaseMsg
		if err := json.Unmarshal(env.Payload, &msg); err != nil {
			return err
		}
		a.releaseJob(msg.JobID)
	}
	return nil
}

// releaseJob flips a held job to queued; the normal per-printer worker then
// picks it up and the existing download→verify→print path runs unchanged.
func (a *Agent) releaseJob(jobID string) {
	ok, err := a.queue.Release(jobID)
	if err != nil {
		log.Printf("release job %s: %v", jobID, err)
		return
	}
	if !ok {
		// Unknown id, or already released/printed — harmless, just note it.
		log.Printf("release job %s: no held job with that id", jobID)
		return
	}
	log.Printf("released job %s to print queue", jobID)
}

// persistJob writes a pushed job to the queue and acks it. It does NOT print —
// release-mode jobs (v1 default) are held until a release message arrives;
// print_now jobs go straight to queued. Either way a per-printer worker later
// claims the job via GetNext and prints it. This preserves write-before-print:
// the durable record exists before any print.
func (a *Agent) persistJob(job protocol.Job) {
	err := a.queue.Enqueue(job)
	switch {
	case err == queue.ErrDuplicate:
		a.sendAck(job, true) // already seen → ack, do not requeue/reprint
	case err != nil:
		// A real persist failure (e.g. disk error): we can't safely print a job
		// we didn't record, so surface it. Ack as non-duplicate so the cloud
		// isn't told it was deduped.
		log.Printf("persist job %s: %v", job.ID, err)
		a.sendAck(job, false)
	default:
		a.sendAck(job, false)
	}
}

// printJob runs the print half of the lifecycle for an already-claimed job:
// download + verify checksum → print on printerName → record and report status.
func (a *Agent) printJob(job protocol.Job, printerName string) {
	// Download the PDF to a local temp file (verifying its checksum) before
	// handing it to the printer.
	path, cleanup, err := download.ToTempFile(job.PDFURL, job.PDFSHA256)
	if err != nil {
		_ = a.queue.SetState(job.ID, protocol.StateFailed)
		a.sendStatus(job.ID, protocol.StateFailed)
		return
	}
	defer cleanup()

	// Print the downloaded file on THIS worker's printer.
	state, _ := a.printer.Print(path, printerName)

	// Record and report the outcome.
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
