//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func mqttConnection(t *testing.T) (*tls.Conn, context.CancelFunc, <-chan struct{}, string, *mqttSessions) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "mqtt-wire", filepath.Join(root, "state")
	account, bearer := filepath.Join(root, "account"), filepath.Join(root, "bearer")
	for _, path := range []string{account, bearer} {
		if err := auth.CreateCredential(path); err != nil {
			t.Fatal(err)
		}
	}
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: account}}
	c.Principals = []config.Principal{{ID: "producer", Binding: "native", BearerTokenFile: bearer, Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#fixture"}}}}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(bearer)
	if err != nil {
		t.Fatal(err)
	}
	cert := httptest.NewTLSServer(nil)
	pair, leaf := cert.TLS.Certificates[0], cert.Certificate()
	cert.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	server, peer := net.Pipe()
	secure := tls.Server(server, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})
	client := tls.Client(peer, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "example.com"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	owners := &mqttSessions{active: make(map[string]*mqttSession)}
	routes := map[string]config.MQTTRoute{"airc/send": {Principal: "producer", Target: core.Target{Kind: "room", Name: "#fixture"}}}
	go func() { serveMQTTConnection(ctx, secure, nil, v, make(chan struct{}, 1), routes, owners); close(done) }()
	t.Cleanup(func() { cancel(); _ = client.Close(); <-done })
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return client, cancel, done, token, owners
}

func mqttConnectBytes(username, clientID, token string, keepAlive uint16) []byte {
	body := []byte{0, 4, 'M', 'Q', 'T', 'T', 4, 0xc2, 0, 0}
	binary.BigEndian.PutUint16(body[8:], keepAlive)
	for _, field := range []string{clientID, username, token} {
		body = binary.BigEndian.AppendUint16(body, uint16(len(field)))
		body = append(body, field...)
	}
	packet := []byte{0x10}
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

func TestMQTTTLSConnectPingAndJoinedCancellation(t *testing.T) {
	client, cancel, done, token, owners := mqttConnection(t)
	if _, err := client.Write(mqttConnectBytes("producer", "producer", token, 10)); err != nil {
		t.Fatal(err)
	}
	var reply [4]byte
	if _, err := io.ReadFull(client, reply[:]); err != nil || reply != [4]byte{0x20, 2, 0, 0} {
		t.Fatal("CONNACK", reply, err)
	}
	if _, err := client.Write([]byte{0xc0, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, reply[:2]); err != nil || reply[0] != 0xd0 || reply[1] != 0 {
		t.Fatal("PINGRESP", reply, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("TLS connection not joined")
	}
	owners.mu.Lock()
	defer owners.mu.Unlock()
	if len(owners.active) != 0 {
		t.Fatal("leaked session owner")
	}
}

func TestMQTTTLSConnectAuthorityAndKeepAlive(t *testing.T) {
	for _, kind := range []string{"username", "client_id", "bearer", "keepalive", "partial"} {
		t.Run(kind, func(t *testing.T) {
			client, _, done, token, _ := mqttConnection(t)
			username, clientID, reason := "producer", "producer", byte(0)
			switch kind {
			case "username":
				username, reason = "other", 4
			case "client_id":
				clientID, reason = "other", 2
			case "bearer":
				token, reason = "0000000000000000000000000000000000000000000000000000000000000000", 4
			}
			if _, err := client.Write(mqttConnectBytes(username, clientID, token, 1)); err != nil {
				t.Fatal(err)
			}
			var reply [4]byte
			if _, err := io.ReadFull(client, reply[:]); err != nil || reply != [4]byte{0x20, 2, 0, reason} {
				t.Fatal("CONNACK", reply, err)
			}
			if kind == "partial" {
				if _, err := client.Write([]byte{0xc0}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-time.After(2500 * time.Millisecond):
				t.Fatal("refusal/keepalive failed to close")
			}
		})
	}
}
