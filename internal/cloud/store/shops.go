package store

import (
	"errors"

	"gorm.io/gorm"
)

// ErrCodeUnusable means a setup code was not found or was already consumed.
var ErrCodeUnusable = errors.New("setup code invalid or already used")

// Shops provides shop provisioning and auth queries backed by GORM.
type Shops struct{ db *gorm.DB }

// NewShops wraps a DB handle for shop operations.
func NewShops(db *gorm.DB) *Shops { return &Shops{db: db} }

// Create inserts a shop with a one-time setup code and returns its new id.
func (s *Shops) Create(name, setupCode string) (string, error) {
	var id string
	err := s.db.Raw(
		`INSERT INTO shops (name, setup_code, setup_code_used)
		 VALUES (?, ?, false) RETURNING id`,
		name, setupCode,
	).Scan(&id).Error
	if err != nil {
		return "", err
	}
	return id, nil
}

// Consume atomically exchanges an unused setup code for a token: it stores the
// token hash, marks the code used, clears it, and returns the shop id. It
// returns ErrCodeUnusable if the code is unknown or already consumed.
func (s *Shops) Consume(setupCode, tokenHash string) (string, error) {
	var id string
	err := s.db.Raw(
		`UPDATE shops
		 SET token_hash = ?, setup_code_used = true, setup_code = NULL
		 WHERE setup_code = ? AND setup_code_used = false
		 RETURNING id`,
		tokenHash, setupCode,
	).Scan(&id).Error
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", ErrCodeUnusable
	}
	return id, nil
}

// TokenHash returns the stored token hash for a shop, or "" if the shop is
// unknown or not yet provisioned. An invalid shop id surfaces as an error.
func (s *Shops) TokenHash(shopID string) (string, error) {
	var hash string
	err := s.db.Raw(
		`SELECT COALESCE(token_hash, '') FROM shops WHERE id = ?`, shopID,
	).Scan(&hash).Error
	if err != nil {
		return "", err
	}
	return hash, nil
}
