package protocol

import "time"

type JobState string

const (
	StateHeld      JobState = "held"   // persisted, paid, waiting for the claim code
	StateQueued    JobState = "queued" // persisted, waiting for a worker to claim
	StatePrinting  JobState = "printing"
	StateDone      JobState = "done"
	StateFailed    JobState = "failed"
	StateUncertain JobState = "uncertain"
	StateExpired   JobState = "expired"
)

// DefaultJobType is used when a job arrives without an explicit Type. Every shop
// has at least a mono printer, so mono is the safe backward-compatible default.
const DefaultJobType = "mono"

type JobMode string

const (
	ModePrintNow JobMode = "print_now"
	ModeRelease  JobMode = "release"
)

type ColorMode string

const (
	ColorMono  ColorMode = "mono"
	ColorColor ColorMode = "color"
)

type PrintSettings struct {
	Color     ColorMode `json:"color"`
	Copies    int       `json:"copies"`
	Duplex    bool      `json:"duplex"`
	PaperSize string    `json:"paper_size"`
}

type Job struct {
	ID             string        `json:"id"`
	Type           string        `json:"type,omitempty"` // "mono" | "color"; empty defaults to mono
	ShopID         string        `json:"shop_id"`
	IdempotencyKey string        `json:"idempotency_key"`
	Mode           JobMode       `json:"mode"`
	ClaimCode      string        `json:"claim_code"`
	PDFURL         string        `json:"pdf_url"`
	PDFSHA256      string        `json:"pdf_sha256"`
	Settings       PrintSettings `json:"settings"`
	CreatedAt      time.Time     `json:"created_at"`
	ExpiresAt      time.Time     `json:"expires_at"`
}

// PrinterType returns the printer type this job must be routed to, defaulting an
// empty/unset Type to mono. This keeps the agent working with clouds that don't
// yet send a type. Apply it at the persist boundary so the default lives in one
// place rather than being re-checked by every worker.
func (j Job) PrinterType() string {
	if j.Type == "" {
		return DefaultJobType
	}
	return j.Type
}

// PrintMode returns the job's mode, defaulting an empty/unset Mode to release —
// v1 is hold-for-release, so a job never prints before its claim code is typed
// unless the cloud explicitly asks for print_now.
func (j Job) PrintMode() JobMode {
	if j.Mode == "" {
		return ModeRelease
	}
	return j.Mode
}
