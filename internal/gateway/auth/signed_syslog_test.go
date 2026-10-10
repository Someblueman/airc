//go:build unix

package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testsyslog"
)

func TestSignedSyslogOriginAuthority(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	o := testsyslog.New(t, now.Add(-time.Hour), now.Add(time.Hour))
	c, _ := fixtureConfig(t)
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	options := config.SignedSyslog{Routes: []config.SignedSyslogRoute{{CertificateDER: base64.StdEncoding.EncodeToString(o.DER), Hostname: "signed.example", Principal: "device", Target: c.Principals[0].Targets[0]}}}
	a, err := NewSignedSyslogAuthenticator(v, options)
	if err != nil {
		t.Fatal(err)
	}
	covered := []byte(`<110>1 2026-10-07T00:00:00Z forged.example app 1 event - exact 雪`)
	h := sha256.Sum256(covered)
	unsigned := `<110>1 2026-10-07T00:00:00Z SIGNED.EXAMPLE app 1 audit [ssign VER="0121" RSID="1" SG="0" SPRI="110" GBC="0" FMN="1" CNT="1" HB="` + base64.StdEncoding.EncodeToString(h[:]) + `"]`
	raw := o.Sign(t, unsigned)
	options.Routes[0].Principal = "unknown"
	i, target, pin, b, err := a.VerifyBlock(raw, [][]byte{covered}, now)
	if err != nil || i.ID() != "device" || target != c.Principals[0].Targets[0] || pin == ([32]byte{}) || b.First != 1 {
		t.Fatalf("%s %+v %v", i.ID(), target, err)
	}
	cert, err := config.SignedSyslogCertificate(base64.StdEncoding.EncodeToString(o.DER))
	if err != nil || pin != sha256.Sum256(cert.RawSubjectPublicKeyInfo) {
		t.Fatal("wrong signing-key replay digest", err)
	}
	renewed := testsyslog.WithKey(t, o.Key, now.Add(-2*time.Hour), now.Add(2*time.Hour))
	options.Routes[0].Principal = "device"
	options.Routes[0].CertificateDER = base64.StdEncoding.EncodeToString(renewed.DER)
	rotated, err := NewSignedSyslogAuthenticator(v, options)
	if err != nil {
		t.Fatal(err)
	}
	_, _, renewedPin, _, err := rotated.VerifyBlock(raw, [][]byte{covered}, now)
	if err != nil || renewedPin != pin {
		t.Fatal("certificate renewal reset replay identity", err)
	}
	for _, bad := range [][]byte{o.Sign(t, strings.Replace(unsigned, "SIGNED.EXAMPLE", "unknown.example", 1)), []byte(strings.Replace(string(raw), `FMN="1"`, `FMN="2"`, 1))} {
		if _, _, _, _, err := a.VerifyBlock(bad, [][]byte{covered}, now); err == nil {
			t.Fatal("unauthorized block accepted")
		}
	}
	if _, _, _, _, err := a.VerifyBlock(raw, [][]byte{covered}, now.Add(time.Hour)); err == nil {
		t.Fatal("expired certificate accepted")
	}
	payload := []byte(now.Format(time.RFC3339) + " C " + base64.StdEncoding.EncodeToString(o.DER))
	if started, err := a.VerifyPayload("SIGNED.EXAMPLE", payload); err != nil || !started.Equal(now) {
		t.Fatal(started, err)
	}
	for _, bad := range [][]byte{[]byte("- C " + base64.StdEncoding.EncodeToString(o.DER)), []byte(now.Format(time.RFC3339) + " N"), append(payload, ' ')} {
		if _, err := a.VerifyPayload("signed.example", bad); err == nil {
			t.Fatal("invalid payload accepted")
		}
	}
}
