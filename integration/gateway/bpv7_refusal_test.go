//go:build darwin || linux

package gateway_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestBPv7RefusesMetadataAndFreshnessBeforeJournal(t *testing.T) {
	f := newBPFixture(t)
	now := time.Now().Unix()
	good := bpIntent(t, f, 1, now-10, now+300, "healthy control")
	for _, kind := range []string{"source", "destination", "sequence", "creation", "lifetime", "subsecond_creation", "subsecond_lifetime", "expired", "future", "crc", "size", "wrong_id"} {
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
			case "wrong_id":
				r := core.Request{Version: 1, RequestID: "unbound-id", Operation: core.Send, Target: &f.config.Listeners[0].BundleProtocol.Target, Message: "unbound"}
				p.payload = f.credentials.validityToken(t, r, now-10, now+300, false)
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
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(native) != 0 {
		t.Fatal("refusal delivered", native)
	}
	if report := bpCall(t, f, good.wire(), true); len(report.Entries) != 1 || report.Entries[0].Result.State != core.Accepted {
		t.Fatal(report)
	}
}
