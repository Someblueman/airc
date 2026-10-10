//go:build unix

package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

func syslogClient(t *testing.T, expired, serverOnly bool) tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(3), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if expired {
		cert.NotAfter = time.Now().Add(-time.Minute)
	}
	if serverOnly {
		cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	raw, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}
}

func TestSyslogPinnedPeerHandshakeAndGrant(t *testing.T) {
	serverCert := testtls.New(t)
	for _, name := range []string{"valid", "unknown", "absent", "wrong_key", "expired", "server_only", "TLS12"} {
		t.Run(name, func(t *testing.T) {
			pair := syslogClient(t, name == "expired", name == "server_only")
			digest := sha256.Sum256(pair.Certificate[0])
			c, _ := fixtureConfig(t)
			v, err := New(c)
			if err != nil {
				t.Fatal(err)
			}
			options := config.Syslog{Routes: []config.SyslogRoute{{CertificateSHA256: hex.EncodeToString(digest[:]), Principal: "device", Target: c.Principals[0].Targets[0]}}}
			a, err := NewSyslogAuthenticator(v, options)
			if err != nil {
				t.Fatal(err)
			}
			// The constructor owns route authority; subsequent caller mutation is inert.
			options.Routes[0].Target = core.Target{Kind: "room", Name: "#other"}
			cfg, err := a.TLSConfig(serverCert.Server)
			if err != nil {
				t.Fatal(err)
			}
			clientCfg := serverCert.Client.Clone()
			clientCfg.ServerName = "localhost"
			clientCfg.Certificates = []tls.Certificate{pair}
			switch name {
			case "unknown":
				clientCfg.Certificates = []tls.Certificate{syslogClient(t, false, false)}
			case "absent":
				clientCfg.Certificates = nil
			case "wrong_key":
				_, key, e := ed25519.GenerateKey(rand.Reader)
				if e != nil {
					t.Fatal(e)
				}
				clientCfg.Certificates[0].PrivateKey = key
			case "TLS12":
				clientCfg.MinVersion = tls.VersionTLS12
				clientCfg.MaxVersion = tls.VersionTLS12
			}
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			deadline := time.Now().Add(2 * time.Second)
			left.SetDeadline(deadline)
			right.SetDeadline(deadline)
			server := tls.Server(left, cfg)
			client := tls.Client(right, clientCfg)
			type result struct {
				identity Identity
				target   core.Target
				err      error
			}
			done := make(chan result, 1)
			go func() {
				id, target, e := a.Authenticate(context.Background(), server)
				if e == nil {
					_, e = server.Write([]byte{1})
				}
				left.Close()
				done <- result{id, target, e}
			}()
			clientErr := client.Handshake()
			if clientErr == nil {
				var b [1]byte
				_, clientErr = client.Read(b[:])
			}
			got := <-done
			if name == "valid" {
				if got.err != nil || clientErr != nil || got.identity.ID() != "device" || got.identity.Binding() != "fixture-backend" || got.target != c.Principals[0].Targets[0] {
					t.Fatalf("wrong grant: %+v client=%v", got, clientErr)
				}
			} else if got.err == nil || got.identity.ID() != "" || got.target != (core.Target{}) {
				t.Fatalf("unauthorized handshake: %+v", got)
			}
		})
	}
}

func TestSyslogGrantConstructionRefusesExpandedAuthority(t *testing.T) {
	c, _ := fixtureConfig(t)
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	route := config.SyslogRoute{CertificateSHA256: strings.Repeat("a", 64), Principal: "device", Target: c.Principals[0].Targets[0]}
	for name, mutate := range map[string]func(*config.Syslog){
		"empty":     func(o *config.Syslog) { o.Routes = nil },
		"duplicate": func(o *config.Syslog) { o.Routes = append(o.Routes, o.Routes[0]) },
		"unknown":   func(o *config.Syslog) { o.Routes[0].Principal = "unknown" },
		"target":    func(o *config.Syslog) { o.Routes[0].Target.Name = "#other" },
		"pin":       func(o *config.Syslog) { o.Routes[0].CertificateSHA256 = "bad" },
	} {
		t.Run(name, func(t *testing.T) {
			o := config.Syslog{Routes: []config.SyslogRoute{route}}
			mutate(&o)
			if _, e := NewSyslogAuthenticator(v, o); e == nil {
				t.Fatal("expanded grant accepted")
			}
		})
	}
	c.Principals[0].Revoked = true
	revoked, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSyslogAuthenticator(revoked, config.Syslog{Routes: []config.SyslogRoute{route}}); err == nil {
		t.Fatal("revoked grant accepted")
	}
	if _, err := NewSyslogAuthenticator(nil, config.Syslog{Routes: []config.SyslogRoute{route}}); err == nil {
		t.Fatal("nil verifier accepted")
	}
}
