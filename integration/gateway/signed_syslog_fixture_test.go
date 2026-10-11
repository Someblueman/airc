//go:build darwin || linux

package gateway_test

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testsyslog"
)

type signedSyslogFixture struct {
	*syslogFixture
	origin  testsyslog.Origin
	started time.Time
	signer  [32]byte
}

func prepareSignedSyslogFixture(t *testing.T) *signedSyslogFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	f := &signedSyslogFixture{syslogFixture: prepareSyslogFixture(t), started: now}
	f.origin = testsyslog.New(t, now.Add(-time.Hour), now.Add(time.Hour))
	cert, err := x509.ParseCertificate(f.origin.DER)
	if err != nil {
		t.Fatal(err)
	}
	f.signer = sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	l := &f.config.Listeners[0]
	l.Protocol = "syslog_signed"
	l.SignedSyslog = &config.SignedSyslog{Routes: []config.SignedSyslogRoute{{CertificateDER: base64.StdEncoding.EncodeToString(f.origin.DER), Hostname: "signed.example", BoundAuthor: config.BoundAuthor{Nick: fixtureAuthorNick, AccountTokenFile: fixtureAuthorTokenFile(t, f.config, "signed-syslog-author")}, Target: core.Target{Kind: "room", Name: "#gateway-fixture"}}}}
	return f
}
func signedSyslogHeader(at time.Time) string {
	return "<110>1 " + at.Format(time.RFC3339) + " signed.example app 1 audit "
}
func (f *signedSyslogFixture) certificate(t *testing.T, peer *tls.Conn) {
	t.Helper()
	payload := f.started.Format(time.RFC3339) + " C " + base64.StdEncoding.EncodeToString(f.origin.DER)
	raw := f.origin.Sign(t, signedSyslogHeader(time.Now())+fmt.Sprintf(`[ssign-cert VER="0121" RSID="1" SG="0" SPRI="110" TPBL="%d" INDEX="1" FLEN="%d" FRAG="%s"]`, len(payload), len(payload), payload))
	syslogNativeWrite(t, peer, raw)
}
func (f *signedSyslogFixture) block(t *testing.T, counter, first int, records ...[]byte) []byte {
	t.Helper()
	hashes := make([]string, len(records))
	for i, raw := range records {
		digest := sha256.Sum256(raw)
		hashes[i] = base64.StdEncoding.EncodeToString(digest[:])
	}
	return f.origin.Sign(t, signedSyslogHeader(time.Now())+fmt.Sprintf(`[ssign VER="0121" RSID="1" SG="0" SPRI="110" GBC="%d" FMN="%d" CNT="%d" HB="%s"]`, counter, first, len(records), strings.Join(hashes, " ")))
}
func (f *signedSyslogFixture) requestID(t *testing.T, number uint64, raw []byte) string {
	t.Helper()
	data, err := json.Marshal(struct {
		Protocol       string
		Signer         [32]byte
		Hostname       string
		Reboot, Number uint64
		Record         []byte
	}{"syslog_signed/record-v1", f.signer, "signed.example", 1, number, raw})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
