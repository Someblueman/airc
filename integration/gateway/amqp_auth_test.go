//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestAMQPTLSAndSASLAccessRefusals(t *testing.T) {
	f := prepareAMQPFixture(t)
	f.start(t)
	wrongName, legacy := f.cert.Client.Clone(), f.cert.Client.Clone()
	wrongName.ServerName = "wrong.example"
	legacy.MinVersion, legacy.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	for name, trust := range map[string]*tls.Config{"untrusted": {MinVersion: tls.VersionTLS13}, "wrong_name": wrongName, "legacy": legacy} {
		t.Run(name, func(t *testing.T) {
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, trust)
			if err == nil {
				_ = conn.NetConn().Close()
				t.Fatal("AMQP accepted invalid TLS")
			}
		})
	}
	for _, kind := range []string{"password", "anonymous", "mechanism"} {
		t.Run(kind, func(t *testing.T) {
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, f.cert.Client.Clone())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.NetConn().Close()
			header := []byte{'A', 'M', 'Q', 'P', 3, 1, 0, 0}
			aqWrite(t, conn, header)
			if err := conn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
				t.Fatal(err)
			}
			reply := make([]byte, 8)
			if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != string(header) {
				t.Fatal(reply, err)
			}
			if p := aqRead(t, conn); p.kind != 0x40 {
				t.Fatal(p)
			}
			mechanism, user, password, authorization := "PLAIN", "anyone", f.bearer, ""
			switch kind {
			case "password":
				password = strings.Repeat("0", 64)
			case "anonymous":
				// The listener requires its password.
				mechanism = "ANONYMOUS"
			case "mechanism":
				mechanism = "SCRAM-SHA-256"
			}
			aqWrite(t, conn, aqFrame(0x41, 1, [][]byte{aqBytes(0xb3, []byte(mechanism)), aqBytes(0xb0, []byte(authorization+"\x00"+user+"\x00"+password))}, nil))
			if p := aqRead(t, conn); p.kind != 0x44 || aqField(p, 0).number != 1 {
				t.Fatal("SASL access refusal", p)
			}
		})
	}
	healthy := f.connect(t)
	aqWrite(t, healthy.conn, aqFrame(0x18, 0, nil, nil))
	if p := aqRead(t, healthy.conn); p.kind != 0x18 {
		t.Fatal(p)
	}
	f.stop(t)
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatal(messages)
	}
}
