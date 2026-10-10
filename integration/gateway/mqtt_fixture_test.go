//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

type mqttFixture struct {
	*fileMailboxFixture
	process         *gatewayProcess
	address, bearer string
	cert            testtls.Certificate
}

func newMQTTFixture(t *testing.T) *mqttFixture {
	f := prepareMQTTFixture(t)
	f.start(t)
	return f
}

func prepareMQTTFixture(t *testing.T) *mqttFixture {
	t.Helper()
	f := &mqttFixture{fileMailboxFixture: newFileMailboxFixture(t), cert: testtls.New(t)}
	f.config.Listeners = []config.Listener{{ID: "mqtt", Protocol: "mqtt", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: f.cert.CertPath, TLSKeyFile: f.cert.KeyPath, Operations: []core.Operation{core.Send}, MQTT: &config.MQTT{Routes: []config.MQTTRoute{{Principal: f.config.Principals[0].ID, Topic: "airc/send", Target: core.Target{Kind: "room", Name: "#gateway-fixture"}}}}}}
	var err error
	f.bearer, err = auth.ReadToken(f.config.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *mqttFixture) start(t *testing.T) {
	t.Helper()
	f.process = startGateway(t, f.binary, f.config)
	f.address = gatewayAddress(t, f.process)
}

func (f *mqttFixture) connect(t *testing.T) *tls.Conn {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, f.cert.Client.Clone())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.NetConn().Close(); _ = conn.Close() })
	if state := conn.ConnectionState(); state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != "" {
		t.Fatal("MQTT TLS trust/profile", state)
	}
	body := []byte{0, 4, 'M', 'Q', 'T', 'T', 4, 0xc2, 0, 10}
	for _, field := range []string{f.config.Principals[0].ID, f.config.Principals[0].ID, f.bearer} {
		body = mqttString(body, field)
	}
	mqttWrite(t, conn, mqttPacket(0x10, body))
	mqttReply(t, conn, []byte{0x20, 2, 0, 0})
	return conn
}

// This client serializes packets independently of the production MQTT codec.
func mqttString(body []byte, field string) []byte {
	return append(binary.BigEndian.AppendUint16(body, uint16(len(field))), field...)
}
func mqttPacket(header byte, body []byte) []byte {
	packet := []byte{header}
	for size := len(body); ; size /= 128 {
		b := byte(size % 128)
		if size >= 128 {
			b |= 128
		}
		packet = append(packet, b)
		if size < 128 {
			break
		}
	}
	return append(packet, body...)
}
func mqttPublication(id uint16, topic, payload string, duplicate bool) []byte {
	body := binary.BigEndian.AppendUint16(mqttString(nil, topic), id)
	header := byte(0x32)
	if duplicate {
		header |= 8
	}
	return mqttPacket(header, append(body, payload...))
}
func mqttWrite(t *testing.T, conn net.Conn, packet []byte) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(conn, bytes.NewReader(packet)); err != nil {
		t.Fatal(err)
	}
}
func mqttReply(t *testing.T, conn net.Conn, want []byte) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, want) {
		t.Fatal("MQTT reply", got, err)
	}
}
func (f *mqttFixture) stop(t *testing.T) {
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
		t.Fatal("MQTT service did not join")
	}
}

func mqttPeerClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var b [4]byte
	n, err := conn.Read(b[:])
	if n != 0 || !(errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET)) {
		t.Fatal("MQTT peer did not close without acknowledgement", b, n, err)
	}
}
