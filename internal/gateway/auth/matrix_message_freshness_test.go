//go:build unix

package auth

import (
	"testing"
	"time"
)

func checkMatrixFingerprint(t *testing.T, m VerifiedMatrixMessage, origin int64, window time.Duration) {
	t.Helper()
	occurred := time.UnixMilli(origin).UTC()
	if !m.Occurred().Equal(occurred) || !m.ReplayDeadline().Equal(occurred.Add(window)) || m.Fingerprint() == "" {
		t.Fatal("origin deadline or content fingerprint lost")
	}
	for _, mutate := range []func(*VerifiedMatrixMessage){
		func(v *VerifiedMatrixMessage) { v.body += " " },
		func(v *VerifiedMatrixMessage) { v.author = "@other:localhost" },
		func(v *VerifiedMatrixMessage) { v.room = "!other" },
		func(v *VerifiedMatrixMessage) { v.origin++ },
		func(v *VerifiedMatrixMessage) { v.target.Name += "other" },
	} {
		other := m
		mutate(&other)
		if other.RequestID() != m.RequestID() || other.Fingerprint() == m.Fingerprint() {
			t.Fatal("global identity or immutable content comparison changed")
		}
	}
	var zero VerifiedMatrixMessage
	if !zero.Occurred().IsZero() || !zero.ReplayDeadline().IsZero() || zero.Fingerprint() != "" {
		t.Fatal("zero proof supplied freshness or content authority")
	}
}
