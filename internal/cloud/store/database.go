// Package store handles the cloud's PostgreSQL connection and migrations.
package store

import (
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Connect opens a GORM connection to PostgreSQL using env vars.
func Connect() (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=Asia/Kolkata",
		env("DB_HOST", "localhost"),
		env("DB_USER", "postgres"),
		env("DB_PASSWORD", "postgres"),
		env("DB_NAME", "printos"),
		env("DB_PORT", "5432"),
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return db, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
