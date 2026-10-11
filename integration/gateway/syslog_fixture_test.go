//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

type syslogFixture struct {
	*fileMailboxFixture
	process        *gatewayProcess
	cert           testtls.Certificate
	client         tls.Certificate
	address        string
	receiptAddress string
	receiptCert    testtls.Certificate
}

func prepareSyslogFixture(t *testing.T) *syslogFixture {
	t.Helper()
	f := &syslogFixture{fileMailboxFixture: newFileMailboxFixture(t), cert: testtls.New(t)}
	f.client = syslogFixtureClient(t)
	raw := f.client.Certificate[0]
	pin := sha256.Sum256(raw)
	f.config.Listeners = []config.Listener{{ID: "syslog", Protocol: "syslog_tls", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: f.cert.CertPath, TLSKeyFile: f.cert.KeyPath, Operations: []core.Operation{core.Send}, Syslog: &config.Syslog{Routes: []config.SyslogRoute{{CertificateSHA256: hex.EncodeToString(pin[:]), BoundAuthor: config.BoundAuthor{Nick: fixtureAuthorNick, AccountTokenFile: fixtureAuthorTokenFile(t, f.config, "syslog-author")}, Target: core.Target{Kind: "room", Name: "#gateway-fixture"}}}}}}
	f.receiptCert = testtls.New(t)
	f.config.Listeners = append(f.config.Listeners, config.Listener{ID: "receipts", Protocol: "http", Enabled: true, Address: ipv4LoopbackAlias, TLSCertFile: f.receiptCert.CertPath, TLSKeyFile: f.receiptCert.KeyPath, Operations: []core.Operation{core.ReceiptGet}})
	return f
}
func (f *syslogFixture) start(t *testing.T) {
	t.Helper()
	f.process = startGateway(t, f.binary, f.config)
	select {
	case data := <-f.process.ready:
		var ready struct {
			Listeners []struct{ ID, Address string }
		}
		if e := json.Unmarshal(data, &ready); e != nil || len(ready.Listeners) != 2 {
			t.Fatal("bad readiness", string(data), e)
		}
		for _, l := range ready.Listeners {
			if l.ID == "syslog" {
				f.address = l.Address
			}
			if l.ID == "receipts" {
				f.receiptAddress = l.Address
			}
		}
		if f.address == "" || f.receiptAddress == "" {
			t.Fatal("missing listener address")
		}
	case <-f.process.done:
		t.Fatal("gateway exited", f.process.err, f.process.stderr.String())
	case <-time.After(8 * time.Second):
		t.Fatal("gateway readiness timed out")
	}
}
func (f *syslogFixture) stop(t *testing.T) {
	t.Helper()
	if e := f.process.command.Process.Signal(os.Interrupt); e != nil {
		t.Fatal(e)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal(f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("syslog service not joined")
	}
}
func (f *syslogFixture) connect(t *testing.T) *tls.Conn {
	t.Helper()
	c := f.cert.Client.Clone()
	c.ServerName = "localhost"
	c.Certificates = []tls.Certificate{f.client}
	peer, e := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, c)
	if e != nil {
		t.Fatal(e)
	}
	if peer.ConnectionState().Version != tls.VersionTLS13 || peer.ConnectionState().NegotiatedProtocol != "" {
		t.Fatal("wrong TLS profile")
	}
	peer.SetDeadline(time.Now().Add(10 * time.Second))
	t.Cleanup(func() { peer.NetConn().Close() })
	return peer
}
func syslogNativeRecord(event, body string) []byte {
	return []byte("<34>1 2026-10-07T00:00:00Z forged-author app 42 " + event + " - " + body)
}
func syslogNativeWrite(t *testing.T, peer *tls.Conn, raw []byte) {
	t.Helper()
	wire := append([]byte(strconv.Itoa(len(raw))+" "), raw...)
	if n, e := peer.Write(wire); e != nil || n != len(wire) {
		t.Fatal("incomplete TLS write", n, e)
	}
}
func syslogNativeID(t *testing.T, f *syslogFixture, raw []byte) string {
	t.Helper()
	data, e := json.Marshal(struct {
		Protocol, Realm string
		Target          core.Target
		Record          []byte
	}{"syslog_tls/content-v1", f.config.Realm, f.config.Listeners[0].Syslog.Routes[0].Target, raw})
	if e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func (f *syslogFixture) accepted(t *testing.T, raw []byte, messageID string) {
	t.Helper()
	f.acceptedID(t, syslogNativeID(t, f, raw), messageID)
}
func (f *syslogFixture) acceptedID(t *testing.T, requestID, messageID string) {
	t.Helper()
	receiptTLS := f.receiptCert.Client.Clone()
	receiptTLS.ServerName = "localhost"
	transport := &http.Transport{TLSClientConfig: receiptTLS, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var result core.Result
	for {
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+f.receiptAddress+"/v1/requests/"+requestID, nil)
		if e != nil {
			t.Fatal(e)
		}
		// The bound author's job is read as that author.
		request.Header.Set("AIRC-Nick", fixtureAuthorNick)
		request.Header.Set("AIRC-Token", fixtureAccount)
		response, e := client.Do(request)
		if e != nil {
			// Closing the sender does not synchronously release its server-side
			// slot. The single-slot fixture may briefly refuse receipt admission.
			if !errors.Is(e, io.EOF) && !errors.Is(e, syscall.ECONNRESET) {
				t.Fatal("receipt control failed", e)
			}
		} else {
			result = core.Result{}
			decode := json.NewDecoder(response.Body).Decode(&result)
			response.Body.Close()
			if response.StatusCode != 200 || decode != nil {
				t.Fatal("receipt control failed", response.StatusCode, decode)
			}
			if ctx.Err() == nil && result.State == core.Accepted && result.Durable && result.SendAttempts == 1 && result.MessageID == messageID {
				return
			}
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("receipt admission or durable acceptance did not settle", result, e)
		case <-timer.C:
		}
	}
}

func syslogFixtureClient(t *testing.T) tls.Certificate {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(99), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	raw, e := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}
}
