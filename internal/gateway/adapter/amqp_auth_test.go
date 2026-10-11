//go:build darwin || linux

package adapter

import (
	"crypto/tls"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
)

func TestAMQPSASLPlainCarriesTheListenerPassword(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, challenge := range []bool{false, true} {
			t.Run(map[bool]string{false: "plain", true: "TLS"}[secure]+"/"+map[bool]string{false: "initial", true: "challenge"}[challenge], func(t *testing.T) {
				client, server, _, done := amqpAuthFixture(t, secure, "letmein")
				amqpClientMechanisms(t, client, "PLAIN")
				// The user name is not an identity.
				response := []byte("\x00anyone\x00letmein")
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
				if result := amqpAuthFinished(t, done); result.err != nil {
					t.Fatal(result.err)
				}
				// A successful return leaves the connection open for frames.
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
}

func TestAMQPOpenListenerAdmitsAnonymousAndNoSASL(t *testing.T) {
	t.Run("anonymous", func(t *testing.T) {
		client, _, _, done := amqpAuthFixture(t, false, "")
		amqpClientMechanisms(t, client, "ANONYMOUS", "PLAIN")
		if err := writeAMQPSASL(client, amqpwire.SASLPerformative{Kind: 0x41, Fields: []amqpwire.Value{{Code: 0xa3, Data: []byte("ANONYMOUS")}}}); err != nil {
			t.Fatal(err)
		}
		if p, err := readAMQPSASL(client); err != nil || p.Kind != 0x44 || p.Fields[0].Number != 0 {
			t.Fatal(p, err)
		}
		amqpClientHeader(t, client, amqpwire.AMQPProtocol)
		if result := amqpAuthFinished(t, done); result.err != nil {
			t.Fatal(result.err)
		}
	})
	t.Run("no_sasl", func(t *testing.T) {
		client, _, _, done := amqpAuthFixture(t, false, "")
		amqpClientHeader(t, client, amqpwire.AMQPProtocol)
		if result := amqpAuthFinished(t, done); result.err != nil {
			t.Fatal(result.err)
		}
	})
}

func TestAMQPSecurityLayerRefusals(t *testing.T) {
	for _, kind := range []string{"wrong_password", "empty_password", "anonymous_with_password", "mechanism", "extra_nul"} {
		t.Run(kind, func(t *testing.T) {
			client, _, _, done := amqpAuthFixture(t, false, "letmein")
			amqpClientMechanisms(t, client, "PLAIN")
			mechanism, password := "PLAIN", "letmein"
			switch kind {
			case "wrong_password":
				password = "nope"
			case "empty_password":
				password = ""
			case "anonymous_with_password":
				mechanism = "ANONYMOUS"
			case "mechanism":
				mechanism = "SCRAM-SHA-256"
			case "extra_nul":
				password += "\x00x"
			}
			if err := writeAMQPSASL(client, amqpwire.SASLPerformative{Kind: 0x41, Fields: []amqpwire.Value{{Code: 0xa3, Data: []byte(mechanism)}, {Code: 0xa0, Data: []byte("\x00user\x00" + password)}}}); err != nil {
				t.Fatal(err)
			}
			p, err := readAMQPSASL(client)
			if err != nil || p.Kind != 0x44 || p.Fields[0].Number != 1 {
				t.Fatal(p, err)
			}
			if result := amqpAuthFinished(t, done); !errors.Is(result.err, auth.ErrAuthentication) {
				t.Fatal(result.err)
			}
		})
	}
	t.Run("sasl_bypass_with_password", func(t *testing.T) {
		client, _, _, done := amqpAuthFixture(t, false, "letmein")
		if _, err := client.Write([]byte{'A', 'M', 'Q', 'P', 0, 1, 0, 0}); err != nil {
			t.Fatal(err)
		}
		// The server answers with the SASL header it requires, then closes.
		var header [8]byte
		if _, err := io.ReadFull(client, header[:]); err != nil || header != [8]byte{'A', 'M', 'Q', 'P', 3, 1, 0, 0} {
			t.Fatal(header, err)
		}
		if result := amqpAuthFinished(t, done); !errors.Is(result.err, auth.ErrForbidden) {
			t.Fatal(result.err)
		}
	})
	t.Run("unknown_header", func(t *testing.T) {
		client, _, _, done := amqpAuthFixture(t, false, "")
		if _, err := client.Write([]byte{'A', 'M', 'Q', 'P', 0, 0, 9, 1}); err != nil {
			t.Fatal(err)
		}
		var header [8]byte
		if _, err := io.ReadFull(client, header[:]); err != nil || header != [8]byte{'A', 'M', 'Q', 'P', 0, 1, 0, 0} {
			t.Fatal(header, err)
		}
		if result := amqpAuthFinished(t, done); !errors.Is(result.err, amqpwire.ErrHeader) {
			t.Fatal(result.err)
		}
	})
	t.Run("wrong_performative", func(t *testing.T) {
		client, _, _, done := amqpAuthFixture(t, false, "")
		amqpClientMechanisms(t, client, "ANONYMOUS", "PLAIN")
		if err := writeAMQPSASL(client, amqpwire.SASLPerformative{Kind: 0x43, Fields: []amqpwire.Value{{Code: 0xa0}}}); err != nil {
			t.Fatal(err)
		}
		if result := amqpAuthFinished(t, done); !errors.Is(result.err, auth.ErrAuthentication) {
			t.Fatal(result.err)
		}
	})
	t.Run("tls12", func(t *testing.T) {
		_, _, _, done := amqpAuthFixtureVersion(t, true, "", tls.VersionTLS12)
		if result := amqpAuthFinished(t, done); result.err == nil {
			t.Fatal("TLS 1.2 accepted")
		}
	})
}

func TestAMQPSecurityLayerJoinedCancellation(t *testing.T) {
	for _, stage := range []string{"header", "init", "amqp_header"} {
		t.Run(stage, func(t *testing.T) {
			client, _, cancel, done := amqpAuthFixture(t, true, "")
			if stage != "header" {
				amqpClientMechanisms(t, client, "ANONYMOUS", "PLAIN")
			}
			if stage == "amqp_header" {
				if err := writeAMQPSASL(client, amqpwire.SASLPerformative{Kind: 0x41, Fields: []amqpwire.Value{{Code: 0xa3, Data: []byte("ANONYMOUS")}}}); err != nil {
					t.Fatal(err)
				}
				if p, err := readAMQPSASL(client); err != nil || p.Kind != 0x44 {
					t.Fatal(p, err)
				}
			}
			cancel()
			if result := amqpAuthFinished(t, done); result.err == nil {
				t.Fatal("cancelled phase admitted")
			}
			var b [1]byte
			if _, err := client.Read(b[:]); err == nil {
				t.Fatal("cancelled stream remains open")
			}
		})
	}
}

func TestAMQPPlainPasswordGrammar(t *testing.T) {
	for _, response := range [][]byte{[]byte("user"), []byte("\x00user"), []byte("\x00user\x00a\x00b"), []byte(strings.Repeat("a", 256) + "\x00user\x00pw"), []byte("\x00\xff\x00pw"), []byte("\x00user\x00bad\nline")} {
		if _, err := amqpPlainPassword(response); err == nil {
			t.Fatalf("unsafe PLAIN response %q", response)
		}
	}
	if p, err := amqpPlainPassword([]byte("authz\x00user\x00secret")); err != nil || p.Empty() {
		t.Fatal(err)
	}
	if p, err := amqpPlainPassword([]byte("\x00user\x00")); err != nil || !p.Empty() {
		t.Fatal("empty password is none", err)
	}
}
