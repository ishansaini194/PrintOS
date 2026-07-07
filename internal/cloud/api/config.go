package api

import (
	"log"
	"os"
	"time"
)

const (
	defaultHoldTTL       = 30 * time.Minute
	defaultSweepInterval = 5 * time.Minute
)

func holdTTL() time.Duration {
	return durationEnv("PRINTOS_HOLD_TTL", defaultHoldTTL)
}

func expirySweepInterval() time.Duration {
	return durationEnv("PRINTOS_EXPIRY_SWEEP_INTERVAL", defaultSweepInterval)
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("invalid %s=%q; using %s", key, v, fallback)
		return fallback
	}
	return d
}
