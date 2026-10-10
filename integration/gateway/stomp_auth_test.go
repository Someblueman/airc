//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSTOMPTLSAndCONNECTAuthenticationRefusals(t *testing.T) {
	f := prepareSTOMPFixture(t)
	f.start(t)
	wrongName, legacy := f.cert.Client.Clone(), f.cert.Client.Clone()
	wrongName.ServerName = "wrong.example"
	legacy.MinVersion, legacy.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	for name, trust := range map[string]*tls.Config{"untrusted": {MinVersion: tls.VersionTLS13}, "wrong_name": wrongName, "legacy": legacy} {
		t.Run(name, func(t *testing.T) {
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, trust)
			if err == nil {
				_ = conn.NetConn().Close()
				t.Fatal("STOMP accepted invalid TLS")
			}
		})
	}
	for _, kind := range []string{"password", "username", "realm", "version"} {
		t.Run(kind, func(t *testing.T) {
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, f.cert.Client.Clone())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.NetConn().Close()
			if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
				t.Fatal(err)
			}
			version, realm, user, password := "1.2", f.config.Realm, f.config.Principals[0].ID, f.bearer
			switch kind {
			case "password":
				password = strings.Repeat("0", 64)
			case "username":
				user = "unknown"
			case "realm":
				realm = "other"
			case "version":
				version = "1.1"
			}
			sqWrite(t, conn, "CONNECT", []string{"accept-version:" + version, "host:" + realm, "login:" + user, "passcode:" + password, "heart-beat:0,0"}, nil)
			sqClosed(t, conn, bufio.NewReader(conn))
		})
	}
	// Negotiate only server heartbeats and observe an actual EOL on the wire.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, f.cert.Client.Clone())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.NetConn().Close()
	if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	sqWrite(t, conn, "CONNECT", []string{"accept-version:1.2", "host:" + f.config.Realm, "login:" + f.config.Principals[0].ID, "passcode:" + f.bearer, "heart-beat:0,1000"}, nil)
	if frame, err := sqDecode(reader); err != nil || frame.command != "CONNECTED" || frame.headers["heart-beat"] != "1000,1000" {
		t.Fatal(frame, err)
	}
	if heartbeat, err := reader.ReadByte(); err != nil || heartbeat != '\n' {
		t.Fatal("missing negotiated heartbeat", heartbeat, err)
	}
	sqWrite(t, conn, "DISCONNECT", []string{"receipt:bye"}, nil)
	if frame, err := sqDecode(reader); err != nil || frame.command != "RECEIPT" || frame.headers["receipt-id"] != "bye" {
		t.Fatal(frame, err)
	}
	sqClosed(t, conn, reader)
	f.stop(t)
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatal(messages)
	}
}
