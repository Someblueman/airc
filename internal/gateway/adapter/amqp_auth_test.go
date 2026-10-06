//go:build darwin || linux

package adapter

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"io"
	"net"
	"strings"
	"testing"
)

func TestAMQPTLSPlainAuthentication(t *testing.T) {
	for _, challenge := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "challenge"}[challenge], func(t *testing.T) {
			client, server, _, done, token, _ := amqpAuthFixture(t, true)
			amqpClientMechanisms(t, client)
			response := []byte("\x00producer\x00" + token)
			p := amqpwire.SASLPerformative{Kind: 0x41, Fields: []amqpwire.Value{{Code: 0xa3, Data: []byte("PLAIN")}}}
			if !challenge {
				p.Fields = append(p.Fields, amqpwire.Value{Code: 0xa0, Data: response})
			}
			if err := writeAMQPSASL(client, p); err != nil {
				t.Fatal(err)
			}
			if challenge {
				p, err := readAMQPSASL(client)
				if err != nil || p.Kind != 0x42 || len(p.Fields[0].Data) != 0 {
					t.Fatal(p, err)
				}
				if err := writeAMQPSASL(client, amqpwire.SASLPerformative{Kind: 0x43, Fields: []amqpwire.Value{{Code: 0xa0, Data: response}}}); err != nil {
					t.Fatal(err)
				}
			}
			p, err := readAMQPSASL(client)
			if err != nil || p.Kind != 0x44 || p.Fields[0].Number != 0 {
				t.Fatal(p, err)
			}
			amqpClientHeader(t, client, amqpwire.AMQPProtocol)
			result := amqpAuthFinished(t, done)
			if result.err != nil || result.identity.ID() != "producer" || result.identity.Binding() != "native" {
				t.Fatal(result)
			}
			// Successful helper return must leave the connection open for frames.
			written := make(chan error, 1)
			go func() { _, err := client.Write([]byte{0x99}); written <- err }()
			var b [1]byte
			if _, err := io.ReadFull(server, b[:]); err != nil || b[0] != 0x99 {
				t.Fatal("stream closed or consumed frame", err)
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAMQPTLSPlainAuthorityRefusals(t *testing.T) {
	for _, kind := range []string{"username", "authz", "bearer", "mechanism", "route", "extra_nul", "empty_password"} {
		t.Run(kind, func(t *testing.T) {
			client, _, _, done, token, _ := amqpAuthFixture(t, kind != "route")
			amqpClientMechanisms(t, client)
			username, authz, mechanism := "producer", "", "PLAIN"
			switch kind {
			case "username":
				username = "other"
			case "authz":
				authz = "other"
			case "bearer":
				token = strings.Repeat("0", 64)
			case "mechanism":
				mechanism = "ANONYMOUS"
			case "extra_nul":
				token += "\x00"
			case "empty_password":
				token = ""
			}
			if err := writeAMQPSASL(client, amqpwire.SASLPerformative{Kind: 0x41, Fields: []amqpwire.Value{{Code: 0xa3, Data: []byte(mechanism)}, {Code: 0xa0, Data: []byte(authz + "\x00" + username + "\x00" + token)}}}); err != nil {
				t.Fatal(err)
			}
			p, err := readAMQPSASL(client)
			if err != nil || p.Kind != 0x44 || p.Fields[0].Number != 1 {
				t.Fatal(p, err)
			}
			result := amqpAuthFinished(t, done)
			if !errors.Is(result.err, auth.ErrAuthentication) || result.identity.ID() != "" {
				t.Fatal(result)
			}
		})
	}
}

func TestAMQPAuthenticationJoinedCancellation(t *testing.T) {
	client, _, cancel, done, _, _ := amqpAuthFixture(t, true)
	// Leave the peer idle at the first SASL header write.
	cancel()
	result := amqpAuthFinished(t, done)
	if result.err == nil || result.identity.ID() != "" {
		t.Fatal(result)
	}
	var b [1]byte
	if _, err := client.Read(b[:]); err == nil {
		t.Fatal("cancelled TLS stream remains open")
	}
}

func TestAMQPPlainGrammar(t *testing.T) {
	_, _, cancel, done, token, v := amqpAuthFixture(t, true)
	cancel()
	amqpAuthFinished(t, done)
	for _, response := range [][]byte{[]byte("producer"), []byte("\x00\x00" + token), []byte("\x00producer\x00short"), []byte("\x00producer\x00" + token + "\x00x"), []byte(strings.Repeat("a", 256) + "\x00producer\x00" + token), []byte("\x00\xff\x00" + token)} {
		if identity, err := authenticateAMQPPlain(v, response); err == nil || identity.ID() != "" {
			t.Fatal("unsafe PLAIN identity")
		}
	}
	if identity, err := authenticateAMQPPlain(v, []byte("producer\x00producer\x00"+token)); err != nil || identity.ID() != "producer" {
		t.Fatal(identity, err)
	}
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	if identity, err := authenticateAMQP(t.Context(), server, v, []config.AMQPRoute{{Principal: "producer"}}); err == nil || identity.ID() != "" {
		t.Fatal("cleartext authenticated")
	}
}
