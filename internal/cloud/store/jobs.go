package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrNotFound is returned when a job lookup matches nothing.
var ErrNotFound = errors.New("job not found")

// Cloud-side job lifecycle (v1 hold-for-release):
// awaiting_payment → paid → held → printing → done | failed.
const (
	JobAwaitingPayment = "awaiting_payment" // created at upload, not yet paid
	JobPaid            = "paid"             // payment confirmed, pushed to agent
	JobHeld            = "held"             // agent acked — job is on the shop PC, waiting for the claim code
)

// Job mirrors a row in the jobs table. state carries the cloud-side lifecycle
// above, then the agent-reported protocol states (printing/done/failed/uncertain).
type Job struct {
	ID             string    `gorm:"column:id"`
	ShopID         string    `gorm:"column:shop_id"`
	IdempotencyKey string    `gorm:"column:idempotency_key"`
	Mode           string    `gorm:"column:mode"`
	State          string    `gorm:"column:state"`
	ClaimCode      string    `gorm:"column:claim_code"`
	Type           string    `gorm:"column:type"` // "mono" | "color"
	Copies         int       `gorm:"column:copies"`
	Pages          int       `gorm:"column:pages"`
	AmountPaise    int       `gorm:"column:amount_paise"`
	PDFSHA256      string    `gorm:"column:pdf_sha256"`
	Duplex         bool      `gorm:"column:duplex"`
	PaperSize      string    `gorm:"column:paper_size"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
	ExpiresAt      time.Time `gorm:"column:expires_at"`
}

// NewJob carries everything needed to create a job row at upload time.
type NewJob struct {
	ShopID         string
	IdempotencyKey string
	ClaimCode      string
	Type           string // "mono" | "color"
	Copies         int
	Pages          int
	AmountPaise    int
	Duplex         bool
	PaperSize      string
	ExpiresAt      time.Time
}

// JobStore provides job persistence backed by GORM.
type JobStore struct{ db *gorm.DB }

// NewJobStore wraps a DB handle for job operations.
func NewJobStore(db *gorm.DB) *JobStore { return &JobStore{db: db} }

// Create inserts a release-mode job in the 'awaiting_payment' state and returns
// the stored row (with its generated id and timestamps). Nothing is sent to the
// agent until payment is confirmed.
func (s *JobStore) Create(p NewJob) (Job, error) {
	var j Job
	err := s.db.Raw(
		`INSERT INTO jobs (shop_id, idempotency_key, mode, state, claim_code,
		                   type, copies, pages, amount_paise, duplex, paper_size, expires_at)
		 VALUES (?, ?, 'release', ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING *`,
		p.ShopID, p.IdempotencyKey, JobAwaitingPayment, p.ClaimCode,
		p.Type, p.Copies, p.Pages, p.AmountPaise, p.Duplex, p.PaperSize, p.ExpiresAt,
	).Scan(&j).Error
	if err != nil {
		return Job{}, err
	}
	return j, nil
}

// Get returns the job with the given id, or ErrNotFound.
func (s *JobStore) Get(id string) (Job, error) {
	var j Job
	res := s.db.Raw(`SELECT * FROM jobs WHERE id = ?`, id).Scan(&j)
	if res.Error != nil {
		return Job{}, res.Error
	}
	if res.RowsAffected == 0 {
		return Job{}, ErrNotFound
	}
	return j, nil
}

// SetState updates a job's state (called when the agent reports status).
func (s *JobStore) SetState(id, state string) error {
	return s.db.Exec(
		`UPDATE jobs SET state = ?, updated_at = now() WHERE id = ?`, state, id,
	).Error
}

// SetSHA records the stored PDF's checksum on the job row (set at upload, sent
// to the agent at payment time).
func (s *JobStore) SetSHA(id, sha string) error {
	return s.db.Exec(
		`UPDATE jobs SET pdf_sha256 = ?, updated_at = now() WHERE id = ?`, sha, id,
	).Error
}

// MarkHeld records that the agent acked the pushed job — but only from 'paid',
// so a late/duplicate ack never regresses a job that is already printing/done.
func (s *JobStore) MarkHeld(id string) error {
	return s.db.Exec(
		`UPDATE jobs SET state = ?, updated_at = now() WHERE id = ? AND state = ?`,
		JobHeld, id, JobPaid,
	).Error
}

// FindReleasable returns the shop's non-expired job with the given claim code
// that is paid or held (i.e. safe to print), or ErrNotFound. An unpaid job can
// never be released.
func (s *JobStore) FindReleasable(shopID, claimCode string) (Job, error) {
	var j Job
	res := s.db.Raw(
		`SELECT * FROM jobs
		 WHERE shop_id = ? AND claim_code = ? AND state IN (?, ?) AND expires_at > now()
		 ORDER BY created_at LIMIT 1`,
		shopID, claimCode, JobPaid, JobHeld,
	).Scan(&j)
	if res.Error != nil {
		return Job{}, res.Error
	}
	if res.RowsAffected == 0 {
		return Job{}, ErrNotFound
	}
	return j, nil
}

// ClaimCodeActive reports whether the shop already has an active (not done or
// failed, not expired) job with this claim code — used to keep codes unambiguous
// at release time.
func (s *JobStore) ClaimCodeActive(shopID, claimCode string) (bool, error) {
	var count int64
	err := s.db.Raw(
		`SELECT COUNT(*) FROM jobs
		 WHERE shop_id = ? AND claim_code = ?
		   AND state NOT IN ('done', 'failed') AND expires_at > now()`,
		shopID, claimCode,
	).Scan(&count).Error
	return count > 0, err
}
