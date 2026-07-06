package protocol

import (
	"encoding/json"
	"time"
)

// MessageType tags every message on the wire.
type MessageType string

const (
	// Cloud → Agent
	MsgJobPush   MessageType = "job_push"
	MsgRelease   MessageType = "release" // print a held job (claim code was typed)
	MsgResolve   MessageType = "resolve"
	MsgUpdateNow MessageType = "update_now"

	// Agent → Cloud
	MsgHello     MessageType = "hello" // agent identifies its shop on connect
	MsgJobAck    MessageType = "job_ack"
	MsgStatus    MessageType = "status"
	MsgHeartbeat MessageType = "heartbeat"

	// Customer-originated, relayed by cloud
	MsgReportProblem MessageType = "report_problem"
)

// Envelope wraps every message so the receiver can dispatch on Type and reject
// mismatched protocol versions.
type Envelope struct {
	Type            MessageType     `json:"type"`
	ProtocolVersion string          `json:"protocol_version"`
	SentAt          time.Time       `json:"sent_at"`
	Payload         json.RawMessage `json:"payload"`
}

// --- Cloud → Agent ---

type JobPushMsg struct {
	Job Job `json:"job"`
}

// ReleaseMsg tells the agent to print a held job — sent when someone types the
// job's claim code on the shop's release page.
type ReleaseMsg struct {
	JobID string `json:"job_id"`
}

type ResolveMsg struct {
	JobID      string   `json:"job_id"`
	Resolution JobState `json:"resolution"` // done or failed
	ByOwner    bool     `json:"by_owner"`
}

// --- Agent → Cloud ---

// HelloMsg is the first message an agent sends after connecting, identifying
// which shop it is. Token is reserved for authentication (empty in v1).
type HelloMsg struct {
	ShopID string `json:"shop_id"`
	Token  string `json:"token,omitempty"`
}

// JobAckMsg confirms the agent wrote the job to disk BEFORE printing.
type JobAckMsg struct {
	JobID          string `json:"job_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Duplicate      bool   `json:"duplicate"` // already seen → acked, not reprinted
}

type StatusMsg struct {
	JobID  string    `json:"job_id"`
	State  JobState  `json:"state"`
	Detail string    `json:"detail,omitempty"` // spooler reason on failure
	At     time.Time `json:"at"`
}

type PrinterStatus string

const (
	PrinterReady      PrinterStatus = "ready"
	PrinterOffline    PrinterStatus = "offline"
	PrinterOutOfPaper PrinterStatus = "out_of_paper"
	PrinterJam        PrinterStatus = "jam"
	PrinterLowToner   PrinterStatus = "low_toner"
	PrinterUnknown    PrinterStatus = "unknown"
)

type HeartbeatMsg struct {
	AgentVersion  string        `json:"agent_version"`
	PrinterStatus PrinterStatus `json:"printer_status"`
	QueueDepth    int           `json:"queue_depth"`
	At            time.Time     `json:"at"`
}

type ReportProblemMsg struct {
	JobID     string `json:"job_id"`
	ClaimCode string `json:"claim_code"`
	Note      string `json:"note,omitempty"`
}
