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
	"testing"
	"time"
)

// mqttConnection serves one MQTT connection over a pipe, with TLS when
// secure, on a listener whose access password is password (none if empty).
func mqttConnection(t *testing.T, secure bool, password string) (net.Conn, context.CancelFunc, <-chan struct{}, *mqttSessions, *routeFixture) {
	t.Helper()
	f := newRouteFixture(t)
	l := mqttFixtureListener()
	l.PasswordFile = password
	d := f.listener(t, l)
	server, peer := net.Pipe()
	var client net.Conn = peer
	var serverConn net.Conn = server
	var tlsClient *tls.Conn
	if secure {
		cert := httptest.NewTLSServer(nil)
		pair, leaf := cert.TLS.Certificates[0], cert.Certificate()
		cert.Close()
		roots := x509.NewCertPool()
		roots.AddCert(leaf)
		serverConn = tls.Server(server, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})
		tlsClient = tls.Client(peer, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "example.com"})
		client = tlsClient
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	owners := &mqttSessions{active: make(map[string]*mqttSession)}
	go func() {
		serveMQTTConnection(ctx, serverConn, d, make(chan struct{}, 1), mqttFixtureRoutes(), owners, newMQTT5Store())
		close(done)
	}()
	t.Cleanup(func() { cancel(); _ = client.Close(); <-done })
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if tlsClient != nil {
		if err := tlsClient.HandshakeContext(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return client, cancel, done, owners, f
}

func mqttRemaining(packet []byte, body []byte) []byte {
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

// mqttConnectBytes builds CONNECT with a clean session; an empty username
// and password omit them.
func mqttConnectBytes(username, clientID, password string, keepAlive uint16) []byte {
	flags := byte(0x02)
	if username != "" {
		flags |= 0x80
	}
	if password != "" {
		flags |= 0x40
	}
	body := []byte{0, 4, 'M', 'Q', 'T', 'T', 4, flags, 0, 0}
	binary.BigEndian.PutUint16(body[8:], keepAlive)
	fields := []string{clientID}
	if username != "" {
		fields = append(fields, username)
	}
	if password != "" {
		fields = append(fields, password)
	}
	for _, field := range fields {
		body = binary.BigEndian.AppendUint16(body, uint16(len(field)))
		body = append(body, field...)
	}
	return mqttRemaining([]byte{0x10}, body)
}

func mqttPublishBytes(qos byte, id uint16, topic, payload string) []byte {
	body := binary.BigEndian.AppendUint16(nil, uint16(len(topic)))
	body = append(body, topic...)
	if qos == 1 {
		body = binary.BigEndian.AppendUint16(body, id)
	}
	return mqttRemaining([]byte{0x30 | qos<<1}, append(body, payload...))
}

func TestMQTTConnectPublishPingAndJoinedCancellation(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "TLS"}[secure], func(t *testing.T) {
			client, cancel, done, owners, f := mqttConnection(t, secure, "")
			// No credentials and no client ID: a clean session gets one.
			if _, err := client.Write(mqttConnectBytes("", "", "", 0)); err != nil {
				t.Fatal(err)
			}
			var reply [4]byte
			if _, err := io.ReadFull(client, reply[:]); err != nil || reply != [4]byte{0x20, 2, 0, 0} {
				t.Fatal("CONNACK", reply, err)
			}
			// QoS 0 is admitted at most once with no acknowledgement; QoS 1
			// is acknowledged after durable intake.
			if _, err := client.Write(mqttPublishBytes(0, 0, "airc/send", plainRouteSend)); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Write(mqttPublishBytes(1, 9, "airc/send", `{"version":1,"request_id":"second","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"hi","author":{"nick":"route-guest"}}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(client, reply[:]); err != nil || reply != [4]byte{0x40, 2, 0, 9} {
				t.Fatal("PUBACK", reply, err)
			}
			if f.jobs(t, "plain-route") != 1 || f.jobs(t, "second") != 1 {
				t.Fatal("publications not admitted")
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
				t.Fatal("connection not joined")
			}
			owners.mu.Lock()
			defer owners.mu.Unlock()
			if len(owners.active) != 0 {
				t.Fatal("leaked session owner")
			}
		})
	}
}

func TestMQTTConnectPasswordAndKeepAlive(t *testing.T) {
	for _, kind := range []string{"password", "wrong_password", "missing_password", "keepalive", "partial", "refused_publish", "will"} {
		t.Run(kind, func(t *testing.T) {
			password := ""
			if kind != "keepalive" && kind != "partial" && kind != "refused_publish" && kind != "will" {
				password = "letmein"
			}
			client, _, done, _, _ := mqttConnection(t, false, password)
			// User names carry no authority; the password is the listener's.
			username, sent, reason := "anyone", "letmein", byte(0)
			switch kind {
			case "wrong_password":
				sent, reason = "nope", 4
			case "missing_password":
				username, sent, reason = "", "", 4
			case "keepalive", "partial", "refused_publish", "will":
				sent = ""
			}
			connect := mqttConnectBytes(username, "client-1", sent, 1)
			if kind == "will" {
				connect[9] |= 0x04
			}
			if _, err := client.Write(connect); err != nil {
				t.Fatal(err)
			}
			var reply [4]byte
			if kind == "will" {
				if _, err := io.ReadFull(client, reply[:]); err == nil {
					t.Fatal("will accepted", reply)
				}
				return
			}
			if _, err := io.ReadFull(client, reply[:]); err != nil || reply != [4]byte{0x20, 2, 0, reason} {
				t.Fatal("CONNACK", reply, err)
			}
			switch kind {
			case "password":
				return
			case "partial":
				if _, err := client.Write([]byte{0xc0}); err != nil {
					t.Fatal(err)
				}
			case "refused_publish":
				// MQTT 3.1.1 has no negative acknowledgement: the connection closes.
				if _, err := client.Write(mqttPublishBytes(1, 1, "airc/send", "not json")); err != nil {
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
