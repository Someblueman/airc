//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

type amqpFixture struct {
	*fileMailboxFixture
	process             *gatewayProcess
	address, authorSpec string
	cert                testtls.Certificate
}

func prepareAMQPFixture(t *testing.T) *amqpFixture {
	t.Helper()
	f := &amqpFixture{fileMailboxFixture: newFileMailboxFixture(t), cert: testtls.New(t)}
	ops := []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}
	// SASL PLAIN carries the listener access password; each request names
	// its author (the fixture's registered account).
	f.authorSpec = "amqp listener password"
	password := filepath.Join(filepath.Dir(f.config.StateDir), "amqp.password")
	if err := os.WriteFile(password, []byte(f.authorSpec+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.config.Listeners = []config.Listener{{ID: "amqp", Protocol: "amqp", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: f.cert.CertPath, TLSKeyFile: f.cert.KeyPath, PasswordFile: password, Operations: ops, Targets: []core.Target{{Kind: "room", Name: "#gateway-fixture"}}, AMQP: &config.AMQP{Routes: []config.AMQPRoute{{RequestAddress: "request", ResponseAddress: "response", Operations: ops}}}}}
	return f
}

// amqpPlainBody is the contract-v1 JSON request an HTTP client would send,
// naming the fixture's registered author.
func amqpPlainBody(t *testing.T, r core.Request) []byte {
	t.Helper()
	return authoredRequest(t, r)
}
func (f *amqpFixture) start(t *testing.T) {
	t.Helper()
	f.process = startGateway(t, f.binary, f.config)
	f.address = gatewayAddress(t, f.process)
}
func (f *amqpFixture) stop(t *testing.T) {
	t.Helper()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal(f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AMQP service did not join")
	}
}

type amqpPeer struct {
	conn                                                *tls.Conn
	nextIncoming, nextOutgoing, delivery, responseCount uint32
}

func (f *amqpFixture) connect(t *testing.T) *amqpPeer {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, f.cert.Client.Clone())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.NetConn().Close() })
	if state := conn.ConnectionState(); state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != "" {
		t.Fatal(state)
	}
	peer := &amqpPeer{conn: conn, nextIncoming: 1, nextOutgoing: 7, delivery: 70}
	for _, protocol := range []byte{3, 0} {
		header := []byte{'A', 'M', 'Q', 'P', protocol, 1, 0, 0}
		aqWrite(t, conn, header)
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		echo := make([]byte, 8)
		if _, err := io.ReadFull(conn, echo); err != nil || string(echo) != string(header) {
			t.Fatal(echo, err)
		}
		if protocol == 3 {
			if p := aqRead(t, conn); p.kind != 0x40 {
				t.Fatal(p)
			}
			aqWrite(t, conn, aqFrame(0x41, 1, [][]byte{aqBytes(0xb3, []byte("PLAIN")), aqBytes(0xb0, []byte("\x00"+fixtureAuthor+"\x00"+f.authorSpec))}, nil))
			if p := aqRead(t, conn); p.kind != 0x44 || aqField(p, 0).number != 0 {
				t.Fatal(p)
			}
		}
	}
	aqWrite(t, conn, aqFrame(0x10, 0, [][]byte{aqBytes(0xb1, []byte("independent-client")), {0x40}, aqUint(512)}, nil))
	if p := aqRead(t, conn); p.kind != 0x10 {
		t.Fatal(p)
	}
	aqWrite(t, conn, aqFrame(0x11, 0, [][]byte{{0x40}, aqUint(7), aqUint(8), aqUint(8)}, nil))
	if p := aqRead(t, conn); p.kind != 0x11 {
		t.Fatal(p)
	}
	// The response link first: the request link's credit then follows both.
	for _, incoming := range []bool{false, true} {
		role, snd, handle, name, source, target := byte(0x42), byte(0), uint32(1), "request-link", "", "request"
		if !incoming {
			role, snd, handle, name, source, target = 0x41, 2, 0, "response-link", "response", ""
		}
		fields := [][]byte{aqBytes(0xb1, []byte(name)), aqUint(handle), {role}, {0x50, snd}, {0x50, 0}, aqDescribed(0x28, aqList(aqBytes(0xb1, []byte(source)))), aqDescribed(0x29, aqList(aqBytes(0xb1, []byte(target)))), {0x40}, {0x42}, aqUint(0)}
		aqWrite(t, conn, aqFrame(0x12, 0, fields, nil))
		if p := aqRead(t, conn); p.kind != 0x12 {
			t.Fatal(p)
		}
	}
	if p := aqRead(t, conn); p.kind != 0x13 || aqField(p, 6).number != 1 {
		t.Fatal(p)
	}
	return peer
}
func (p *amqpPeer) flow(t *testing.T) {
	aqWrite(t, p.conn, aqFrame(0x13, 0, [][]byte{aqUint(p.nextIncoming), aqUint(8), aqUint(p.nextOutgoing), aqUint(8), aqUint(0), aqUint(p.responseCount), aqUint(1)}, nil))
}
