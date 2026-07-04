package protocol

import (
	"encoding/json"
	"time"
)

type MessageType string

const (
	// Cloud → Agent
	MsgJobPush   MessageType = "job_push"
	MsgResolve   MessageType = "resolve"
	MsgUpdateNow MessageType = "update_now"

	// Agent → Cloud
	MsgJobAck    MessageType = "job_ack"
	MsgStatus    MessageType = "status"
	MsgHeartbeat MessageType = "heartbeat"

	// Customer-originated, relayed by cloud
	MsgReportProblem MessageType = "report_problem"
)

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

type ResolveMsg struct {
	JobID      string   `json:"job_id"`
	Resolution JobState `json:"resolution"` // done or failed
	ByOwner    bool     `json:"by_owner"`
}

// --- Agent → Cloud ---

type JobAckMsg struct {
	JobID          string `json:"job_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Duplicate      bool   `json:"duplicate"`
}

type StatusMsg struct {
	JobID  string    `json:"job_id"`
	State  JobState  `json:"state"`
	Detail string    `json:"detail,omitempty"`
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
