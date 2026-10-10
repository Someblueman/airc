//go:build darwin || linux

package gateway_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBPv7RefusesMetadataSignatureAndFreshnessBeforeJournal(t *testing.T) {
	f := newBPFixture(t)
	now := time.Now().Unix()
	good := bpIntent(t, f, 1, now-10, now+300, "healthy control")
	for _, kind := range []string{"source", "destination", "sequence", "creation", "lifetime", "subsecond_creation", "subsecond_lifetime", "expired", "future", "signature", "crc", "size", "wrong_id", "target", "audience", "principal"} {
		t.Run(kind, func(t *testing.T) {
			p := good
			switch kind {
			case "source":
				p.source = "dtn://other/"
			case "destination":
				p.destination = "dtn://gateway/other"
			case "sequence":
				p.sequence++
			case "creation":
				p.creation -= 1000
			case "lifetime":
				p.lifetime += 1000
			case "subsecond_creation":
				p.creation++
			case "subsecond_lifetime":
				p.lifetime++
			case "expired":
				p = bpIntent(t, f, 2, now-120, now-1, "expired")
			case "future":
				p = bpIntent(t, f, 3, now+30, now+300, "future")
			case "signature":
				p.payload = p.payload[:len(p.payload)-10] + "AAAAAAAAAA"
			case "wrong_id":
				r := core.Request{Version: 1, RequestID: "unbound-id", Operation: core.Send, Target: &f.config.Listeners[0].BundleProtocol.Target, Message: "unbound"}
				p.payload = f.credentials.validityToken(t, r, now-10, now+300, false)
			case "target":
				r := core.Request{Version: 1, RequestID: p.id(), Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#not-authorized"}, Message: "denied"}
				p.payload = f.credentials.validityToken(t, r, now-10, now+300, false)
			case "audience":
				other := *f.credentials
				other.realm = "other-realm"
				r := core.Request{Version: 1, RequestID: p.id(), Operation: core.Send, Target: &f.config.Listeners[0].BundleProtocol.Target, Message: "denied"}
				p.payload = other.validityToken(t, r, now-10, now+300, false)
			case "principal":
				other := f.config.Principals[0]
				other.ID = "other-producer"
				other.BearerTokenFile = ""
				public, _, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				key, err := json.Marshal(map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(f.directory, "other-public.json")
				if err := os.WriteFile(path, key, 0600); err != nil {
					t.Fatal(err)
				}
				other.SigningKeys = []config.SigningKey{{ID: "other-key", PublicKeyFile: path}}
				f.config.Principals = append(f.config.Principals, other)
				f.config.Listeners[0].BundleProtocol.Principal = other.ID
				verifier, err := auth.New(f.config)
				if err != nil {
					t.Fatal("other principal setup invalid", err)
				}
				signed, err := verifier.AuthenticateSigned(p.payload, time.Now())
				if err != nil || signed.Identity.ID() != f.config.Principals[0].ID {
					t.Fatal("expected authenticated original producer", err)
				}
				bpSaveConfig(t, f)
				defer func() {
					f.config.Principals = f.config.Principals[:1]
					f.config.Listeners[0].BundleProtocol.Principal = f.config.Principals[0].ID
					bpSaveConfig(t, f)
				}()
			}
			wire := p.wire()
			if kind == "crc" {
				wire[10] ^= 1
			}
			if kind == "size" {
				wire = []byte(strings.Repeat("x", 65537))
			}
			if report := bpCall(t, f, wire, false); report.Version != 0 {
				t.Fatal("preflight returned admission report", report)
			}
			if _, err := os.Stat(filepath.Join(f.config.StateDir, "journal.sqlite")); !os.IsNotExist(err) {
				t.Fatal("refusal opened journal", err)
			}
		})
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(native) != 0 {
		t.Fatal("refusal delivered", native)
	}
	if report := bpCall(t, f, good.wire(), true); len(report.Entries) != 1 || report.Entries[0].Result.State != core.Accepted {
		t.Fatal(report)
	}
}
