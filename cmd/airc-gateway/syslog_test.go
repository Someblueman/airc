package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testsyslog"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestSyslogDiscoveryDoesNotStartRuntime(t *testing.T) {
	for _, protocol := range []string{"syslog_tls", "syslog_signed"} {
		t.Run(protocol, func(t *testing.T) {
			root := t.TempDir()
			c := config.Defaults()
			c.Version, c.Realm, c.StateDir = 1, "syslog_tls-discovery", filepath.Join(root, "state")
			c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: filepath.Join(root, "account")}}
			target := core.Target{Kind: "room", Name: "#fixture"}
			c.Principals = []config.Principal{{ID: "device", Binding: "native", BearerTokenFile: filepath.Join(root, "bearer"), SigningKeys: []config.SigningKey{{ID: "signer", PublicKeyFile: filepath.Join(root, "signer")}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{target}}}
			cert := testtls.New(t)
			c.Listeners = []config.Listener{{ID: "syslog", Protocol: "syslog_tls", Enabled: true, TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: []core.Operation{core.Send}, Syslog: &config.Syslog{Routes: []config.SyslogRoute{{BoundAuthor: config.BoundAuthor{Nick: "device"}, CertificateSHA256: strings.Repeat("a", 64), Target: target}}}}}
			c.Listeners[0].Protocol = protocol
			if protocol == "syslog_signed" {
				now := time.Now()
				origin := testsyslog.New(t, now.Add(-time.Hour), now.Add(time.Hour))
				c.Listeners[0].SignedSyslog = &config.SignedSyslog{Routes: []config.SignedSyslogRoute{{CertificateDER: base64.StdEncoding.EncodeToString(origin.DER), Hostname: "signed.example", BoundAuthor: config.BoundAuthor{Nick: "device"}, Target: target}}}
			}
			for _, name := range []string{"account", "bearer", "signer"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("discovery-only fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "gateway.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run([]string{"capabilities", "--file", path}, &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"status":"enabled_`+protocol+`_partial"`) || !strings.Contains(out.String(), `"operations":["send"]`) || strings.Contains(out.String(), root) {
				t.Fatal(out.String())
			}
			if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
				t.Fatal("discovery started runtime", err)
			}
		})
	}
}
