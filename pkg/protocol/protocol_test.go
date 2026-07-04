package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestJobStateValidity(t *testing.T) {
	valid := []JobState{StatePrinting, StateDone, StateFailed, StateUncertain}
	for _, s := range valid {
		if string(s) == "" {
			t.Errorf("state %q empty", s)
		}
	}
	if JobState("ready") == StatePrinting {
		t.Error("ready must not be a v1 state")
	}
}

func TestJobModes(t *testing.T) {
	if ModePrintNow != "print_now" {
		t.Error("print_now wrong value")
	}
	if ModeRelease != "release" {
		t.Error("release wrong value")
	}
}

func TestColorModes(t *testing.T) {
	if ColorMono != "mono" || ColorColor != "color" {
		t.Error("color mode values wrong")
	}
}

func TestJobRoundTrip(t *testing.T) {
	j := Job{
		ID:             "j1",
		ShopID:         "s1",
		IdempotencyKey: "k1",
		Mode:           ModePrintNow,
		ClaimCode:      "A7",
		PDFURL:         "https://x/y.pdf",
		PDFSHA256:      "abc",
		Settings:       PrintSettings{Color: ColorMono, Copies: 2, Duplex: true, PaperSize: "A4"},
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().UTC(),
	}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var got Job
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != j.ID || got.Settings.Copies != 2 || got.Mode != ModePrintNow {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestMessageRoundTrips(t *testing.T) {
	msgs := []any{
		JobPushMsg{Job: Job{ID: "j1", Mode: ModePrintNow}},
		JobAckMsg{JobID: "j1", IdempotencyKey: "k1", Duplicate: false},
		StatusMsg{JobID: "j1", State: StateDone, At: time.Now().UTC()},
		HeartbeatMsg{AgentVersion: "1.0.0", PrinterStatus: PrinterReady, QueueDepth: 0, At: time.Now().UTC()},
		ReportProblemMsg{JobID: "j1", ClaimCode: "A7"},
		ResolveMsg{JobID: "j1", Resolution: StateDone, ByOwner: true},
	}
	for i, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			t.Errorf("msg %d marshal: %v", i, err)
		}
		if len(b) == 0 {
			t.Errorf("msg %d empty", i)
		}
	}
}

func TestEnvelopeWrapUnwrap(t *testing.T) {
	inner := StatusMsg{JobID: "j1", State: StateFailed, Detail: "jam", At: time.Now().UTC()}
	payload, _ := json.Marshal(inner)
	env := Envelope{
		Type:            MsgStatus,
		ProtocolVersion: Version,
		SentAt:          time.Now().UTC(),
		Payload:         payload,
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var got Envelope
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != MsgStatus || got.ProtocolVersion != Version {
		t.Errorf("envelope mismatch: %+v", got)
	}
	var innerGot StatusMsg
	if err := json.Unmarshal(got.Payload, &innerGot); err != nil {
		t.Fatal(err)
	}
	if innerGot.State != StateFailed || innerGot.Detail != "jam" {
		t.Errorf("payload mismatch: %+v", innerGot)
	}
}

func TestVersionConstants(t *testing.T) {
	if Version == "" || MinSupportedAgentVersion == "" {
		t.Error("version constants must be set")
	}
}
