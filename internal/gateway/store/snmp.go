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
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
)

// AdmitSNMP atomically admits verified intent with USM engine synchronization,
// permanent engine/user ownership and bounded replay marks. MAC authenticity
// cannot establish UTC recency on first contact; the first admitted trap seeds
// the engine's clock, as required for a non-authoritative USM receiver.
func (s *Store) AdmitSNMP(ctx context.Context, proof auth.VerifiedSNMPNotification, sub Submission, now time.Time) (Job, bool, error) {
	if !proof.Valid() {
		return Job{}, false, auth.ErrAuthentication
	}
	p := proof.Packet()
	n := now.Unix()
	if n < 0 || n > math.MaxInt64-s.config.Retry.LifetimeSeconds-auth.ClockSkewSeconds || p.Boots == math.MaxInt32 {
		return Job{}, false, ErrInvalid
	}
	target := proof.Target()
	// The stable identity ignores envelope message IDs and BER length choices.
	// Reusing an engine/user/boots/time/request-ID tuple with changed content
	// conflicts even if that content would render the same human-readable body.
	key := struct {
		Domain, Engine, User string
		Boots, Time          uint32
		RequestID            int32
	}{"airc-snmp-v3", hex.EncodeToString(p.EngineID), p.User, p.Boots, p.Time, p.RequestID}
	encoded, _ := json.Marshal(key)
	id := sha256.Sum256(encoded)
	content, _ := json.Marshal(struct {
		Notification string
		Uptime       uint32
		Variables    []snmpwire.Variable
	}{p.Notification, p.Uptime, p.Variables})
	digest := sha256.Sum256(content)
	r := core.Request{Version: 1, RequestID: hex.EncodeToString(id[:]), Operation: core.Send, Target: &target, Message: proof.Body()}
	if r.Normalize() != nil || r.Message != proof.Body() || *r.Target != proof.Target() {
		return Job{}, false, ErrInvalid
	}
	fingerprint, err := r.Fingerprint()
	if err != nil {
		return Job{}, false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, false, ErrClosed
	}
	owner, err := s.routeOwner(sub)
	if err != nil || sub.Author != proof.Author().Author {
		return Job{}, false, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return Job{}, false, err
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()
	var boots, clock, latest uint32
	var anchor, lastSeen int64
	err = tx.QueryRowContext(ctx, "SELECT boots,engine_time,latest_received,anchor,last_seen FROM snmp_engines WHERE engine=?", p.EngineID).Scan(&boots, &clock, &latest, &anchor, &lastSeen)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, err
	}
	if known {
		if n < lastSeen || n < anchor || boots == math.MaxInt32 || p.Boots < boots {
			return Job{}, false, ErrInvalid
		}
		// RFC 3414 section 3.2 step 7b synchronizes on higher boots or a higher
		// latest-received time before applying the non-authoritative time window.
		if p.Boots > boots || p.Time > latest {
			boots, clock, latest, anchor = p.Boots, p.Time, p.Time, n
		}
		if uint64(p.Time)+150 < uint64(clock)+uint64(n-anchor) {
			return Job{}, false, ErrInvalid
		}
	} else {
		if err := snmpCapacity(ctx, tx, "snmp_engines", 1024); err != nil {
			return Job{}, false, err
		}
		boots, clock, latest, anchor = p.Boots, p.Time, p.Time, n
	}
	var ownerScope, contextName string
	var oldTarget, contextEngine []byte
	err = tx.QueryRowContext(ctx, "SELECT author_scope,target,context_engine,context_name FROM snmp_owners WHERE engine=? AND user=?", p.EngineID, p.User).Scan(&ownerScope, &oldTarget, &contextEngine, &contextName)
	targetJSON, _ := json.Marshal(target)
	if err == nil {
		if ownerScope != owner.scope || !bytes.Equal(oldTarget, targetJSON) || !bytes.Equal(contextEngine, p.ContextEngineID) || contextName != p.Context {
			return Job{}, false, ErrConflict
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		if err := snmpCapacity(ctx, tx, "snmp_owners", 4096); err != nil {
			return Job{}, false, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO snmp_owners VALUES(?,?,?,?,?,?)", p.EngineID, p.User, owner.scope, targetJSON, p.ContextEngineID, p.Context); err != nil {
			return Job{}, false, err
		}
	} else {
		return Job{}, false, err
	}
	// Prune only below the irreversible received-time watermark, never from
	// elapsed wall time: later clock synchronization must not resurrect replay.
	if _, err := tx.ExecContext(ctx, "DELETE FROM snmp_seen WHERE engine=? AND (boots<? OR (boots=? AND engine_time+150<?))", p.EngineID, boots, boots, latest); err != nil {
		return Job{}, false, err
	}
	var oldDigest []byte
	err = tx.QueryRowContext(ctx, "SELECT digest FROM snmp_seen WHERE request_id=?", r.RequestID).Scan(&oldDigest)
	seen := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, err
	}
	if seen && !bytes.Equal(oldDigest, digest[:]) {
		return Job{}, false, ErrConflict
	}
	if seen {
		// A pruned job is never recreated while its authenticated tuple is valid.
		if _, err := s.readOwnedJob(ctx, tx, owner, r.RequestID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Job{}, false, ErrConflict
			}
			return Job{}, false, err
		}
	} else {
		if err := snmpCapacity(ctx, tx, "snmp_seen", 16384); err != nil {
			return Job{}, false, err
		}
	}
	job, created, err := s.admitSubmitterInTx(ctx, tx, owner, r, 0, n, fingerprint)
	if err != nil {
		return Job{}, false, err
	}
	if !seen {
		if _, err := tx.ExecContext(ctx, "INSERT INTO snmp_seen VALUES(?,?,?,?,?,?)", r.RequestID, p.EngineID, p.User, p.Boots, p.Time, digest[:]); err != nil {
			return Job{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO snmp_engines VALUES(?,?,?,?,?,?) ON CONFLICT(engine) DO UPDATE SET boots=excluded.boots,engine_time=excluded.engine_time,latest_received=excluded.latest_received,anchor=excluded.anchor,last_seen=excluded.last_seen`, p.EngineID, boots, clock, latest, anchor, n); err != nil {
		return Job{}, false, err
	}
	if err := commit(ctx, tx); err != nil {
		return Job{}, false, err
	}
	return job, created, nil
}

func snmpCapacity(ctx context.Context, tx *sql.Tx, table string, limit int) error {
	var count int
	// table is one of the fixed internal names supplied above, never input.
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return ErrCapacity
	}
	return nil
}
