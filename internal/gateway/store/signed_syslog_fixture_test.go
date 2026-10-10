//go:build darwin || linux

package store

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testsyslog"
)

func signedBatchFactory(t *testing.T, c config.Config, now time.Time) func(uint64, uint64, uint64, []string, time.Time, time.Time, ...time.Time) *auth.VerifiedSyslogBatch {
	t.Helper()
	o := testsyslog.New(t, time.Unix(0, 0), now.Add(time.Hour))
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.NewSignedSyslogAuthenticator(v, config.SignedSyslog{Routes: []config.SignedSyslogRoute{{CertificateDER: base64.StdEncoding.EncodeToString(o.DER), Hostname: "signed.example", Principal: "device", Target: c.Principals[0].Targets[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	return func(reboot, counter, first uint64, bodies []string, stamp, started time.Time, recordStamp ...time.Time) *auth.VerifiedSyslogBatch {
		s, err := auth.NewSignedSyslogBuffer(a)
		if err != nil {
			t.Fatal(err)
		}
		header := "<110>1 " + stamp.Format(time.RFC3339Nano) + " signed.example app 1 audit "
		payload := started.Format(time.RFC3339Nano) + " C " + base64.StdEncoding.EncodeToString(o.DER)
		cert := o.Sign(t, header+fmt.Sprintf(`[ssign-cert VER="0121" RSID="%d" SG="0" SPRI="110" TPBL="%d" INDEX="1" FLEN="%d" FRAG="%s"]`, reboot, len(payload), len(payload), payload))
		if _, err := s.Push(cert, now); err != nil {
			t.Fatal(err)
		}
		hashes := make([]string, len(bodies))
		for i, body := range bodies {
			recordHeader := header
			if len(recordStamp) > 0 {
				recordHeader = "<110>1 " + recordStamp[0].Format(time.RFC3339Nano) + " signed.example app 1 audit "
			}
			raw := []byte(recordHeader + "- " + body)
			h := sha256.Sum256(raw)
			hashes[i] = base64.StdEncoding.EncodeToString(h[:])
			if b, err := s.Push(raw, now); err != nil || b != nil {
				t.Fatal(err)
			}
		}
		signature := o.Sign(t, header+fmt.Sprintf(`[ssign VER="0121" RSID="%d" SG="0" SPRI="110" GBC="%d" FMN="%d" CNT="%d" HB="%s"]`, reboot, counter, first, len(bodies), strings.Join(hashes, " ")))
		b, err := s.Push(signature, now)
		if err != nil || b == nil {
			t.Fatal(err)
		}
		return b
	}
}
