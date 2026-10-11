//go:build unix

package auth

import (
	"bytes"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/testsnmp"
)

func snmpFixture(t *testing.T, sha256 bool) (config.SNMP, testsnmp.Trap, []byte) {
	t.Helper()
	key := bytes.Repeat([]byte{0x31}, 32)
	protocol := snmpwire.HMACSHA256192
	if !sha256 {
		key = key[:20]
		protocol = snmpwire.HMACSHA196
	}
	path := filepath.Join(privateTemp(t), "localized")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	engine := []byte{0x80, 0, 0, 1, 2, 3, 4, 5}
	options := config.SNMP{Routes: []config.SNMPRoute{{EngineID: hex.EncodeToString(engine), User: "fixture", Authentication: protocol, AuthenticationKeyFile: path, ContextEngineID: hex.EncodeToString(engine), BoundAuthor: config.BoundAuthor{Nick: "device"}, Target: fixtureTarget, Notifications: []config.SNMPNotification{{OID: "1.3.6.1.4.1.123.1", Name: "Alert", Fields: []config.SNMPField{{OID: "1.3.6.1.4.1.123.2", Name: "Message", Type: "text", Required: true}}}}}}}
	trap := testsnmp.Trap{Engine: engine, User: "fixture", Boots: 2, Time: 1234, MessageID: 42, RequestID: -7, SHA256: sha256, Flags: 1, Fields: []testsnmp.Field{{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 2, 1, 1, 3, 0}, Tag: 3, Application: true, Data: []byte{123}}, {OID: asn1.ObjectIdentifier{1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0}, Tag: 6, Data: []byte{43, 6, 1, 4, 1, 123, 1}}, {OID: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 2}, Tag: 4, Data: []byte("世界\nbody")}}}
	return options, trap, key
}
func TestSNMPPrivateAuthorityAndOwnership(t *testing.T) {
	for _, sha256 := range []bool{false, true} {
		t.Run(map[bool]string{false: "SHA1", true: "SHA256"}[sha256], func(t *testing.T) {
			options, trap, key := snmpFixture(t, sha256)
			a, err := NewSNMPAuthenticator(options)
			if err != nil {
				t.Fatal(err)
			}
			raw := testsnmp.Encode(t, trap, key)
			n, err := a.Verify(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !n.Valid() || n.Author().Author != (core.Author{Nick: "device"}) || n.Target() != options.Routes[0].Target || n.Body() != "SNMP Alert\nMessage: 世界\nbody" {
				t.Fatal("wrong grant or body", n.Body())
			}
			clear(raw)
			options.Routes[0].Notifications[0].Fields[0].Name = "changed"
			options.Routes[0].Target.Name = "#other"
			if err := os.WriteFile(options.Routes[0].AuthenticationKeyFile, []byte(strings.Repeat("0", len(key)*2)), 0600); err != nil {
				t.Fatal(err)
			}
			packet := n.Packet()
			clear(packet.Raw)
			clear(packet.EngineID)
			packet.Variables[0].Bytes[0] = 0
			if n.Packet().User != "fixture" || string(n.Packet().Variables[0].Bytes) != "世界\nbody" {
				t.Fatal("mutable packet alias")
			}
			if _, err := a.Verify(testsnmp.Encode(t, trap, key)); err != nil {
				t.Fatal("snapshot mutated", err)
			}
			var zero VerifiedSNMPNotification
			if zero.Valid() || zero.Author().Valid() || len(zero.Packet().Raw) != 0 {
				t.Fatal("zero proof valid")
			}
			if _, err := (*SNMPAuthenticator)(nil).Verify(nil); !errors.Is(err, ErrAuthentication) {
				t.Fatal(err)
			}
		})
	}
}
func TestSNMPAuthenticatedRefusals(t *testing.T) {
	cases := map[string]func(*testsnmp.Trap){
		"user":           func(p *testsnmp.Trap) { p.User = "other" },
		"engine":         func(p *testsnmp.Trap) { p.Engine = append([]byte(nil), p.Engine...); p.Engine[7]++ },
		"context":        func(p *testsnmp.Trap) { p.Context = "other" },
		"context_engine": func(p *testsnmp.Trap) { p.ContextEngine = []byte{0x80, 0, 0, 1, 2, 3, 4, 6} },
		"notification":   func(p *testsnmp.Trap) { p.Fields[1].Data = []byte{43, 6, 1, 4, 1, 123, 3} },
		"missing":        func(p *testsnmp.Trap) { p.Fields = p.Fields[:2] },
		"unknown":        func(p *testsnmp.Trap) { p.Fields[2].OID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 3} },
		"type":           func(p *testsnmp.Trap) { p.Fields[2].Tag = 2; p.Fields[2].Data = []byte{1} },
		"utf8":           func(p *testsnmp.Trap) { p.Fields[2].Data = []byte{255} },
		"nul":            func(p *testsnmp.Trap) { p.Fields[2].Data = []byte{'a', 0} },
		"normalization":  func(p *testsnmp.Trap) { p.Fields[2].Data = []byte("a\r\nb") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o, p, k := snmpFixture(t, true)
			a, err := NewSNMPAuthenticator(o)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&p)
			if n, err := a.Verify(testsnmp.Encode(t, p, k)); err == nil || n.Valid() {
				t.Fatal("refusal failed")
			}
		})
	}
	o, p, k := snmpFixture(t, true)
	a, err := NewSNMPAuthenticator(o)
	if err != nil {
		t.Fatal(err)
	}
	raw := testsnmp.Encode(t, p, k)
	raw[len(raw)-1] ^= 1
	if _, err := a.Verify(raw); !errors.Is(err, ErrAuthentication) {
		t.Fatal("altered MAC accepted", err)
	}
}
func TestSNMPConstructorRefusals(t *testing.T) {
	cases := map[string]func(*config.SNMP){
		"nil_routes": func(o *config.SNMP) { o.Routes = nil },
		"author":     func(o *config.SNMP) { o.Routes[0].BoundAuthor.Nick = "" },
		"token":      func(o *config.SNMP) { o.Routes[0].BoundAuthor.AccountTokenFile = "/nonexistent/token" },
		"uppercase": func(o *config.SNMP) {
			os.WriteFile(o.Routes[0].AuthenticationKeyFile, []byte(strings.Repeat("A", 64)), 0600)
		},
		"short": func(o *config.SNMP) { os.WriteFile(o.Routes[0].AuthenticationKeyFile, []byte("aa"), 0600) },
		"trailing": func(o *config.SNMP) {
			os.WriteFile(o.Routes[0].AuthenticationKeyFile, []byte(strings.Repeat("a", 64)+"\n\n"), 0600)
		},
		"public": func(o *config.SNMP) { os.Chmod(o.Routes[0].AuthenticationKeyFile, 0644) },
		"symlink": func(o *config.SNMP) {
			path := o.Routes[0].AuthenticationKeyFile
			os.Rename(path, path+".real")
			os.Symlink(path+".real", path)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o, _, _ := snmpFixture(t, true)
			mutate(&o)
			if _, err := NewSNMPAuthenticator(o); err == nil {
				t.Fatal("unsafe grant accepted")
			}
		})
	}
	if _, err := NewSNMPAuthenticator(config.SNMP{}); err == nil {
		t.Fatal("empty grants")
	}
}
