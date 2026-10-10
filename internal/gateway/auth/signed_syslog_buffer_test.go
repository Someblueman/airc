//go:build unix

package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testsyslog"
)

func TestSignedSyslogBufferCoverageFragmentsAndDeadline(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	o := testsyslog.New(t, now.Add(-time.Hour), now.Add(time.Hour))
	c, _ := fixtureConfig(t)
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewSignedSyslogAuthenticator(v, config.SignedSyslog{Routes: []config.SignedSyslogRoute{{CertificateDER: base64.StdEncoding.EncodeToString(o.DER), Hostname: "signed.example", Principal: "device", Target: c.Principals[0].Targets[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	header := `<110>1 2026-10-07T00:00:00Z signed.example app 1 audit `
	payload := now.Format(time.RFC3339) + " C " + base64.StdEncoding.EncodeToString(o.DER)
	fragment := func(index int, text string) []byte {
		return o.Sign(t, header+fmt.Sprintf(`[ssign-cert VER="0121" RSID="1" SG="0" SPRI="110" TPBL="%d" INDEX="%d" FLEN="%d" FRAG="%s"]`, len(payload), index, len(text), text))
	}
	ready := func() *SignedSyslogBuffer {
		s, err := NewSignedSyslogBuffer(a)
		if err != nil {
			t.Fatal(err)
		}
		// Reverse fragment order is legal. Identical overlapping bytes are retained.
		cut := len(payload) / 2
		for _, raw := range [][]byte{fragment(cut+1, payload[cut:]), fragment(cut+1, payload[cut:]), fragment(1, payload[:cut])} {
			b, err := s.Push(raw, now)
			if err != nil || b != nil {
				t.Fatal("certificate fragment", err)
			}
		}
		if !s.Deadline().IsZero() {
			t.Fatal("completed payload retained deadline")
		}
		return s
	}
	record := []byte(header + "- exact 雪")
	h := sha256.Sum256(record)
	signature := o.Sign(t, header+`[ssign VER="0121" RSID="1" SG="0" SPRI="110" GBC="0" FMN="1" CNT="1" HB="`+base64.StdEncoding.EncodeToString(h[:])+`"]`)
	s := ready()
	if b, err := s.Push(record, now); err != nil || b != nil {
		t.Fatal("unverified release", err)
	}
	record[0] = '!'
	b, err := s.Push(signature, now.Add(time.Second))
	if err != nil || b == nil || b.Identity.ID() != "device" || string(b.Records[0]) != header+"- exact 雪" || !b.Started.Equal(now) {
		t.Fatal("verified batch", err)
	}
	if !b.Valid(now) {
		t.Fatal("verified batch invalid")
	}
	b.Records[0][0] = '!'
	if b.Valid(now) {
		t.Fatal("altered batch retained authentication proof")
	}
	if new(VerifiedSyslogBatch).Valid(now) {
		t.Fatal("forged batch accepted")
	}
	for _, name := range []string{"deadline", "count", "bytes", "fragments", "conflict", "without_certificate"} {
		t.Run(name, func(t *testing.T) {
			s := ready()
			var err error
			switch name {
			case "deadline":
				_, err = s.Push([]byte(header+"- one"), now)
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.Push([]byte(header+"- two"), now.Add(2*time.Second))
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.Push([]byte(header+"- three"), now.Add(3*time.Second))
			case "count":
				for i := 0; i < 100; i++ {
					_, err = s.Push([]byte(header+"- event"), now)
					if i < 99 && err != nil {
						t.Fatal(err)
					}
				}
			case "bytes":
				large := []byte(header + "- " + strings.Repeat("x", 32768-len(header)-2))
				for n := 0; n < 33; n++ {
					_, err = s.Push(large, now)
					if n < 32 && err != nil {
						t.Fatal(err)
					}
				}
			case "fragments":
				s, _ = NewSignedSyslogBuffer(a)
				part := fragment(1, payload[:10])
				for n := 0; n < 129; n++ {
					_, err = s.Push(part, now)
					if n < 128 && err != nil {
						t.Fatal(err)
					}
				}
			case "conflict":
				s, _ = NewSignedSyslogBuffer(a)
				_, err = s.Push(fragment(1, payload[:10]), now)
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.Push(fragment(1, strings.Repeat("x", 10)), now)
			case "without_certificate":
				s, _ = NewSignedSyslogBuffer(a)
				_, err = s.Push([]byte(header+"- event"), now)
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.Push(signature, now)
			}
			if err == nil || len(s.records) != 0 || s.payload != nil {
				t.Fatal("refusal did not clear buffer", err)
			}
			if _, err = s.Push(signature, now); err == nil {
				t.Fatal("poisoned owner reused")
			}
		})
	}
}
