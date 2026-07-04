package health

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// fakeSender records envelopes sent to it.
type fakeSender struct {
	mu   sync.Mutex
	sent []protocol.Envelope
}

func (f *fakeSender) Send(e protocol.Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, e)
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeSender) first() protocol.Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[0]
}

func TestHeartbeatFiresAndCarriesData(t *testing.T) {
	fs := &fakeSender{}
	hb := New(fs, 20*time.Millisecond, "1.0.0",
		func() protocol.PrinterStatus { return protocol.PrinterReady },
		func() int { return 3 },
	)

	stop := make(chan struct{})
	go hb.Run(stop)
	time.Sleep(70 * time.Millisecond) // allow ~3 ticks
	close(stop)

	if fs.count() == 0 {
		t.Fatal("no heartbeat sent")
	}

	env := fs.first()
	if env.Type != protocol.MsgHeartbeat {
		t.Errorf("expected heartbeat type, got %s", env.Type)
	}
	var msg protocol.HeartbeatMsg
	if err := json.Unmarshal(env.Payload, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.AgentVersion != "1.0.0" {
		t.Errorf("version = %s", msg.AgentVersion)
	}
	if msg.PrinterStatus != protocol.PrinterReady {
		t.Errorf("status = %s", msg.PrinterStatus)
	}
	if msg.QueueDepth != 3 {
		t.Errorf("depth = %d", msg.QueueDepth)
	}
}

func TestHeartbeatStops(t *testing.T) {
	fs := &fakeSender{}
	hb := New(fs, 20*time.Millisecond, "1.0.0",
		func() protocol.PrinterStatus { return protocol.PrinterReady },
		func() int { return 0 },
	)
	stop := make(chan struct{})
	go hb.Run(stop)
	time.Sleep(30 * time.Millisecond)
	close(stop)

	after := fs.count()
	time.Sleep(60 * time.Millisecond) // no more ticks should land
	if fs.count() != after {
		t.Error("heartbeat kept firing after stop")
	}
}
