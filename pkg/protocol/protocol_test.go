package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestJobStateValidity(t *testing.T) {
	valid := []JobState{StateHeld, StateQueued, StatePrinting, StateDone, StateFailed, StateUncertain}
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
		Type:           "color",
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
	if got.ID != j.ID || got.Type != "color" || got.Settings.Copies != 2 || got.Mode != ModePrintNow {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestJobPrintModeDefault(t *testing.T) {
	// Explicit modes are preserved.
	if got := (Job{Mode: ModePrintNow}).PrintMode(); got != ModePrintNow {
		t.Errorf("explicit print_now: got %q", got)
	}
	if got := (Job{Mode: ModeRelease}).PrintMode(); got != ModeRelease {
		t.Errorf("explicit release: got %q", got)
	}
	// Empty/unset mode defaults to release — v1 is hold-for-release.
	if got := (Job{}).PrintMode(); got != ModeRelease {
		t.Errorf("empty mode: got %q, want release", got)
	}
}

func TestReleaseMsgRoundTrip(t *testing.T) {
	payload, err := json.Marshal(ReleaseMsg{JobID: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	env := Envelope{Type: MsgRelease, ProtocolVersion: Version, SentAt: time.Now().UTC(), Payload: payload}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var gotEnv Envelope
	if err := json.Unmarshal(b, &gotEnv); err != nil {
		t.Fatal(err)
	}
	if gotEnv.Type != MsgRelease {
		t.Errorf("type = %q, want release", gotEnv.Type)
	}
	var got ReleaseMsg
	if err := json.Unmarshal(gotEnv.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.JobID != "j1" {
		t.Errorf("job_id = %q, want j1", got.JobID)
	}
}

func TestJobModeRoundTrip(t *testing.T) {
	b, err := json.Marshal(Job{ID: "j1", Mode: ModeRelease})
	if err != nil {
		t.Fatal(err)
	}
	var got Job
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != ModeRelease {
		t.Errorf("mode = %q, want release", got.Mode)
	}
}

func TestJobPrinterTypeDefault(t *testing.T) {
	// Explicit type is preserved.
	if got := (Job{Type: "color"}).PrinterType(); got != "color" {
		t.Errorf("explicit type: got %q, want color", got)
	}
	// Empty/unset type defaults to mono for backward-compat.
	if got := (Job{}).PrinterType(); got != "mono" {
		t.Errorf("empty type: got %q, want mono", got)
	}
	if DefaultJobType != "mono" {
		t.Errorf("DefaultJobType: got %q, want mono", DefaultJobType)
	}
}

func TestJobTypeOmittedWhenEmpty(t *testing.T) {
	// A job with no Type must still round-trip (old clouds send no type field),
	// and unmarshal back to an empty Type that PrinterType() resolves to mono.
	b, err := json.Marshal(Job{ID: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	var got Job
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "" {
		t.Errorf("expected empty Type, got %q", got.Type)
	}
	if got.PrinterType() != "mono" {
		t.Errorf("expected mono default, got %q", got.PrinterType())
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
