package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/syslogwire"
)

// AdmitSignedSyslog atomically commits all covered jobs and the origin replay
// watermark. Retained overlap can coalesce; a pruned old number never recreates.
func (s *Store) AdmitSignedSyslog(ctx context.Context, batch *auth.VerifiedSyslogBatch, sub Submission, now time.Time) ([]Job, error) {
	if !batch.Valid(now) {
		return nil, auth.ErrAuthentication
	}
	b, err := syslogwire.ParseSignedBlock(batch.Block.Record.Raw)
	if err != nil || b.Certificate || b.Group != 0 || b.Reboot == 0 || len(b.Hashes) != len(batch.Records) || b.First+uint64(len(batch.Records))-1 > 9999999999 {
		return nil, ErrInvalid
	}
	n := now.Unix()
	if n < 0 || n > math.MaxInt64-s.config.Retry.LifetimeSeconds-auth.ClockSkewSeconds || batch.Started.Unix() < 0 || batch.Started.After(now.Add(time.Duration(auth.ClockSkewSeconds)*time.Second)) {
		return nil, ErrInvalid
	}
	blockTime, err := time.Parse(time.RFC3339Nano, b.Record.Timestamp)
	if err != nil || !s.signedSyslogFresh(blockTime, batch.Started, n) {
		return nil, ErrInvalid
	}
	requests := make([]core.Request, len(batch.Records))
	fingerprints := make([]string, len(requests))
	for i, raw := range batch.Records {
		r, err := s.signedSyslogRequest(batch, b, b.First+uint64(i), raw, n)
		if err != nil {
			return nil, err
		}
		requests[i] = r
		fingerprints[i], err = r.Fingerprint()
		if err != nil {
			return nil, ErrInvalid
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	owner, err := s.routeOwner(sub)
	if err != nil || sub.Author != batch.Author.Author {
		return nil, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return nil, err
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	name := strings.ToLower(b.Record.Hostname)
	target, _ := json.Marshal(batch.Target)
	var ownerScope, unused, started string
	var oldTarget, digest []byte
	var reboot, counter, last uint64
	err = tx.QueryRowContext(ctx, `SELECT principal,binding,target,reboot,started,block,last_message,block_digest FROM signed_syslog_state WHERE signer=? AND hostname=?`, batch.Signer[:], name).Scan(&ownerScope, &unused, &oldTarget, &reboot, &started, &counter, &last, &digest)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	blockDigest := sha256.Sum256(b.Signed)
	startedText := batch.Started.UTC().Format(time.RFC3339Nano)
	if known {
		if ownerScope != owner.scope || unused != "" || !bytes.Equal(oldTarget, target) || b.Reboot < reboot {
			return nil, ErrConflict
		}
		if b.Reboot == reboot {
			if started != startedText || b.Counter == counter && !bytes.Equal(digest, blockDigest[:]) {
				return nil, ErrConflict
			}
			if b.Counter <= counter && b.First+uint64(len(requests))-1 > last {
				return nil, ErrConflict
			}
		} else {
			oldStarted, err := time.Parse(time.RFC3339Nano, started)
			if err != nil || batch.Started.Before(oldStarted) {
				return nil, ErrConflict
			}
			last = 0
		}
	} else {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM signed_syslog_state").Scan(&count); err != nil {
			return nil, err
		}
		if count >= 1024 {
			return nil, ErrCapacity
		}
	}
	jobs := make([]Job, len(requests))
	for i, r := range requests {
		if b.First+uint64(i) <= last {
			job, err := s.readOwnedJob(ctx, tx, owner, r.RequestID)
			if errors.Is(err, ErrNotFound) {
				return nil, ErrConflict
			}
			if err != nil {
				return nil, err
			}
			fingerprint, err := job.Request.Fingerprint()
			if err != nil || fingerprint != fingerprints[i] {
				return nil, ErrConflict
			}
			jobs[i] = job
		} else {
			job, _, err := s.admitSubmitterInTx(ctx, tx, owner, r, *r.ExpiresAt+auth.ClockSkewSeconds, n, fingerprints[i])
			if err != nil {
				return nil, err
			}
			jobs[i] = job
		}
	}
	if !known || b.Reboot > reboot || b.Counter > counter {
		end := max(last, b.First+uint64(len(requests))-1)
		_, err = tx.ExecContext(ctx, `INSERT INTO signed_syslog_state VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(signer,hostname) DO UPDATE SET reboot=excluded.reboot,started=excluded.started,block=excluded.block,last_message=excluded.last_message,block_digest=excluded.block_digest`, batch.Signer[:], name, owner.scope, "", target, b.Reboot, startedText, b.Counter, end, blockDigest[:])
		if err != nil {
			return nil, err
		}
	}
	if err := commit(ctx, tx); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) signedSyslogFresh(stamp, started time.Time, n int64) bool {
	issued := stamp.Unix()
	return issued >= 0 && issued <= math.MaxInt64-s.config.Retry.LifetimeSeconds-auth.ClockSkewSeconds && issued <= n+auth.ClockSkewSeconds && issued+s.config.Retry.LifetimeSeconds > n && !stamp.Before(started.Add(-time.Duration(auth.ClockSkewSeconds)*time.Second))
}

func (s *Store) signedSyslogRequest(batch *auth.VerifiedSyslogBatch, b syslogwire.SignedBlock, number uint64, raw []byte, n int64) (core.Request, error) {
	record, err := syslogwire.Parse(raw)
	if err != nil {
		return core.Request{}, ErrInvalid
	}
	stamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil || !s.signedSyslogFresh(stamp, batch.Started, n) {
		return core.Request{}, ErrInvalid
	}
	body := bytes.TrimPrefix(record.Message, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(body) || len(body) > 4096 {
		return core.Request{}, ErrInvalid
	}
	encoded, _ := json.Marshal(struct {
		Protocol       string
		Signer         [32]byte
		Hostname       string
		Reboot, Number uint64
		Record         []byte
	}{"syslog_signed/record-v1", batch.Signer, strings.ToLower(b.Record.Hostname), b.Reboot, number, raw})
	id := sha256.Sum256(encoded)
	issued := stamp.Unix()
	expires := issued + s.config.Retry.LifetimeSeconds
	target := batch.Target
	r := core.Request{Version: 1, RequestID: hex.EncodeToString(id[:]), Operation: core.Send, Target: &target, Message: string(body), IssuedAt: &issued, ExpiresAt: &expires}
	if r.Normalize() != nil || r.Message != string(body) {
		return core.Request{}, ErrInvalid
	}
	return r, nil
}
