package protocol

import "time"

type JobState string

const (
	StatePrinting  JobState = "printing"
	StateDone      JobState = "done"
	StateFailed    JobState = "failed"
	StateUncertain JobState = "uncertain"
)

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
