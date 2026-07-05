package store

import (
	"time"

	"gorm.io/gorm"
)

// Job mirrors a row in the jobs table (migration 0001). state carries the
// cloud-side lifecycle: 'created' when the row is first inserted, then updated
// to the agent-reported protocol states (printing/done/failed/uncertain).
type Job struct {
	ID             string    `gorm:"column:id"`
	ShopID         string    `gorm:"column:shop_id"`
	IdempotencyKey string    `gorm:"column:idempotency_key"`
	Mode           string    `gorm:"column:mode"`
	State          string    `gorm:"column:state"`
	ClaimCode      string    `gorm:"column:claim_code"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
	ExpiresAt      time.Time `gorm:"column:expires_at"`
}

// JobStore provides job persistence backed by GORM.
type JobStore struct{ db *gorm.DB }

// NewJobStore wraps a DB handle for job operations.
func NewJobStore(db *gorm.DB) *JobStore { return &JobStore{db: db} }

// Create inserts a print_now job in the cloud-side 'created' state and returns
// the stored row (with its generated id and timestamps).
func (s *JobStore) Create(shopID, idempotencyKey, claimCode string, expires time.Time) (Job, error) {
	var j Job
	err := s.db.Raw(
		`INSERT INTO jobs (shop_id, idempotency_key, mode, state, claim_code, expires_at)
		 VALUES (?, ?, 'print_now', 'created', ?, ?)
		 RETURNING id, shop_id, idempotency_key, mode, state, claim_code, created_at, updated_at, expires_at`,
		shopID, idempotencyKey, claimCode, expires,
	).Scan(&j).Error
	if err != nil {
		return Job{}, err
	}
	return j, nil
}

// SetState updates a job's state (called when the agent reports status).
func (s *JobStore) SetState(id, state string) error {
	return s.db.Exec(
		`UPDATE jobs SET state = ?, updated_at = now() WHERE id = ?`, state, id,
	).Error
}
