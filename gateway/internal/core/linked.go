package core

import "time"

// LinkedDuration returns the timer value a test build linked into value with
// go build -ldflags=-X, so integration tests can wait out a lease or idle
// bound in seconds. Release builds link nothing and get fallback.
func LinkedDuration(value string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(value); err == nil && d > 0 {
		return d
	}
	return fallback
}
