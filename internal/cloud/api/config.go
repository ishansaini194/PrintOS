package api

import (
	"log"
	"os"
	"strings"
	"time"
)

const (
	defaultHoldTTL       = 30 * time.Minute
	defaultSweepInterval = 5 * time.Minute
	defaultPublicURL     = "http://localhost:8080"
)

// publicURL is the base URL the agent uses to reach the cloud for downloads.
// It comes from PRINTOS_PUBLIC_URL, falling back to the local dev default.
// Any trailing slash is trimmed so links can be joined without doubling it.
func publicURL() string {
	base := os.Getenv("PRINTOS_PUBLIC_URL")
	if base == "" {
		base = defaultPublicURL
	}
	return strings.TrimRight(base, "/")
}

// publicLink joins the configured public base URL with a path, avoiding a
// double slash at the boundary.
func publicLink(path string) string {
	return publicURL() + "/" + strings.TrimLeft(path, "/")
}

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
