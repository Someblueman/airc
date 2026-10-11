package auth

import (
	"bytes"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/syslogwire"
)

const SignedSyslogBufferBytes = 1 << 20

// SignedSyslogBuffer is owned by one connection, not shared between goroutines.
// This selected profile uses SG0 and one hostname/app/procid/reboot per owner.
type SignedSyslogBuffer struct {
	auth                     *SignedSyslogAuthenticator
	deadline                 time.Time
	failed                   bool
	name, app, proc          string
	reboot                   uint64
	started                  time.Time
	payload                  []byte
	seen                     []bool
	fragments, filled, bytes int
	records                  [][]byte
}

func NewSignedSyslogBuffer(a *SignedSyslogAuthenticator) (*SignedSyslogBuffer, error) {
	if a == nil || len(a.grants) == 0 {
		return nil, ErrCredential
	}
	return &SignedSyslogBuffer{auth: a}, nil
}

func (s *SignedSyslogBuffer) Deadline() time.Time { return s.deadline }

// Push returns nil until an entire verified batch exists. Every refusal poisons
// the owner and releases buffered references; callers must close the connection.
func (s *SignedSyslogBuffer) Push(raw []byte, now time.Time) (batch *VerifiedSyslogBatch, err error) {
	if s == nil || s.failed {
		return nil, ErrAuthentication
	}
	defer func() {
		if err != nil {
			s.failed = true
			s.records = nil
			s.payload = nil
			s.seen = nil
		}
	}()
	if !s.deadline.IsZero() && !now.Before(s.deadline) {
		return nil, ErrAuthentication
	}
	if s.deadline.IsZero() {
		s.deadline = now.Add(3 * time.Second)
	}
	b, parseErr := syslogwire.ParseSignedBlock(raw)
	if errors.Is(parseErr, syslogwire.ErrNotSignedBlock) {
		if len(s.records) >= 99 || len(raw) > SignedSyslogBufferBytes-s.bytes {
			return nil, ErrAuthentication
		}
		s.records = append(s.records, bytes.Clone(raw))
		s.bytes += len(raw)
		return nil, nil
	}
	if parseErr != nil || b.Group != 0 || b.Reboot == 0 {
		return nil, ErrAuthentication
	}
	name := strings.ToLower(b.Record.Hostname)
	if s.name != "" && (s.name != name || s.app != b.Record.AppName || s.proc != b.Record.ProcID || s.reboot != b.Reboot) {
		return nil, ErrAuthentication
	}
	if b.Certificate {
		_, _, _, b, err = s.auth.VerifyBlock(raw, nil, now)
		if err != nil {
			return nil, err
		}
		if len(s.records) != 0 || !s.started.IsZero() {
			return nil, ErrAuthentication
		}
		if s.name == "" {
			s.name, s.app, s.proc, s.reboot = name, b.Record.AppName, b.Record.ProcID, b.Reboot
		}
		if s.payload == nil {
			s.payload = make([]byte, b.Total)
			s.seen = make([]bool, b.Total)
		}
		if len(s.payload) != b.Total || s.fragments >= 128 {
			return nil, ErrAuthentication
		}
		s.fragments++
		for i, c := range b.Fragment {
			p := b.Index - 1 + i
			if s.seen[p] && s.payload[p] != c {
				return nil, ErrAuthentication
			}
			if !s.seen[p] {
				s.seen[p] = true
				s.filled++
				s.payload[p] = c
			}
		}
		if s.filled == len(s.payload) {
			s.started, err = s.auth.VerifyPayload(s.name, s.payload)
			if err != nil {
				return nil, err
			}
			s.payload = nil
			s.seen = nil
			s.deadline = time.Time{}
		}
		return nil, nil
	}
	if s.started.IsZero() || len(s.records) == 0 {
		return nil, ErrAuthentication
	}
	i, target, pin, verified, err := s.auth.VerifyBlock(raw, s.records, now)
	if err != nil {
		return nil, err
	}
	grant := s.auth.grants[s.name]
	batch = &VerifiedSyslogBatch{Author: i, Target: target, Signer: pin, Block: verified, Started: s.started, Records: s.records, validFrom: grant.before, validUntil: grant.after}
	batch.proof = batch.digest()
	s.records = nil
	s.bytes = 0
	s.deadline = time.Time{}
	return batch, nil
}
