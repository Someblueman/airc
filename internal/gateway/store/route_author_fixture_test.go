//go:build darwin || linux

package store

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

func routeSubmission(a auth.RouteAuthor) Submission {
	return Submission{Author: a.Author, Credential: a.Credential, Listener: "route", Source: "route:route"}
}

func admitSNMPTest(s *Store, ctx context.Context, p auth.VerifiedSNMPNotification, now time.Time) (Job, bool, error) {
	return s.AdmitSNMP(ctx, p, routeSubmission(p.Author()), now)
}

func admitSignedSyslogTest(s *Store, ctx context.Context, b *auth.VerifiedSyslogBatch, now time.Time) ([]Job, error) {
	var author auth.RouteAuthor
	if b != nil {
		author = b.Author
	}
	return s.AdmitSignedSyslog(ctx, b, routeSubmission(author), now)
}
