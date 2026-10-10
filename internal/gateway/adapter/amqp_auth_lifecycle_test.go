//go:build darwin || linux

package adapter

import (
	"crypto/tls"
	"errors"
	"io"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
)

func TestAMQPAuthenticationCancellationStages(t *testing.T) {
	for _, stage := range []string{"header_read", "init", "response", "amqp_header"} {
		t.Run(stage, func(t *testing.T) {
			client, _, cancel, done, token, _ := amqpAuthFixture(t, true)
			if stage == "header_read" {
				var header [8]byte
				if _, err := io.ReadFull(client, header[:]); err != nil {
					t.Fatal(err)
				}
			} else {
				amqpClientMechanisms(t, client)
				if stage == "response" || stage == "amqp_header" {
					p := amqpwire.SASLPerformative{Kind: 0x41, Fields: []amqpwire.Value{{Code: 0xa3, Data: []byte("PLAIN")}}}
					if stage == "amqp_header" {
						p.Fields = append(p.Fields, amqpwire.Value{Code: 0xa0, Data: []byte("\x00producer\x00" + token)})
					}
					if err := writeAMQPSASL(client, p); err != nil {
						t.Fatal(err)
					}
					p, err := readAMQPSASL(client)
					want := byte(0x42)
					if stage == "amqp_header" {
						want = 0x44
					}
					if err != nil || p.Kind != want {
						t.Fatal(p, err)
					}
					if stage == "amqp_header" {
						var header [8]byte
						if _, err := io.ReadFull(client, header[:]); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			cancel()
			result := amqpAuthFinished(t, done)
			if result.err == nil || result.identity.ID() != "" {
				t.Fatal("cancelled phase authenticated", result)
			}
		})
	}
}

func TestAMQPAuthenticationSecurityLayerRefusals(t *testing.T) {
	t.Run("tls12", func(t *testing.T) {
		_, _, _, done, _, _ := amqpAuthFixtureVersion(t, true, tls.VersionTLS12)
		result := amqpAuthFinished(t, done)
		if !errors.Is(result.err, auth.ErrAuthentication) || result.identity.ID() != "" {
			t.Fatal(result)
		}
	})
	t.Run("sasl_bypass", func(t *testing.T) {
		client, _, _, done, _, _ := amqpAuthFixture(t, true)
		var header [8]byte
		if _, err := io.ReadFull(client, header[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write([]byte{'A', 'M', 'Q', 'P', 0, 1, 0, 0}); err != nil {
			t.Fatal(err)
		}
		result := amqpAuthFinished(t, done)
		if !errors.Is(result.err, amqpwire.ErrHeader) || result.identity.ID() != "" {
			t.Fatal(result)
		}
	})
	t.Run("wrong_performative", func(t *testing.T) {
		client, _, _, done, _, _ := amqpAuthFixture(t, true)
		amqpClientMechanisms(t, client)
		if err := writeAMQPSASL(client, amqpwire.SASLPerformative{Kind: 0x43, Fields: []amqpwire.Value{{Code: 0xa0}}}); err != nil {
			t.Fatal(err)
		}
		result := amqpAuthFinished(t, done)
		if !errors.Is(result.err, auth.ErrAuthentication) || result.identity.ID() != "" {
			t.Fatal(result)
		}
	})
}
