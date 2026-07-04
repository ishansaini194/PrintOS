package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// RunMigrations applies every .sql file in dir (in filename order) that hasn't
// run yet. Applied files are recorded in schema_migrations so each runs once.
// Raw .sql (not GORM auto-migrate) means every schema change is explicit and
// data is never silently dropped.
func RunMigrations(db *gorm.DB, dir string) error {
	if err := db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	).Error; err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files) // 0001_, 0002_, ... apply in order

	for _, f := range files {
		name := filepath.Base(f)

		var count int64
		db.Table("schema_migrations").Where("filename = ?", name).Count(&count)
		if count > 0 {
			continue // already applied
		}

		sqlBytes, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		// Run the file and record it together, so a failure rolls back both.
		err = db.Transaction(func(tx *gorm.DB) error {
			if strings.TrimSpace(string(sqlBytes)) != "" {
				if err := tx.Exec(string(sqlBytes)).Error; err != nil {
					return err
				}
			}
			return tx.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name).Error
		})
		if err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}
