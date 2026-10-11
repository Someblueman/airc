//go:build darwin || linux

package service

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestIRCPreparationFailureDoesNotClaimReadyOrRetainJournal(t *testing.T) {
	for _, kind := range []string{"password", "trust"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _, _ := signedServiceConfig(t)
			c.Listeners = nil
			root := filepath.Dir(c.StateDir)
			password, ca := filepath.Join(root, "irc-password"), filepath.Join(root, "irc-ca")
			body := []byte("private")
			if kind == "password" {
				body = bytes.Repeat([]byte("p"), 1025)
			}
			if err := os.WriteFile(password, body, 0600); err != nil {
				t.Fatal(err)
			}
			b := config.IRCBridge{Enabled: true, ID: "network", Address: "127.0.0.1:6697", TLSServerName: "localhost", Nickname: "bridge", Account: "bridge-account", PasswordFile: password, FreshnessSeconds: 60, Routes: []config.IRCRoute{{Account: "alice", Room: "#external", Principal: c.Principals[0].ID, Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
			if kind == "trust" {
				if err := os.WriteFile(ca, []byte("invalid roots"), 0600); err != nil {
					t.Fatal(err)
				}
				b.TLSCAFile = ca
			}
			c.IRCBridges = []config.IRCBridge{b}
			var out bytes.Buffer
			if err := Run(t.Context(), c, &out); err == nil || out.Len() != 0 {
				t.Fatal("failed preparation claimed ready", err, out.String())
			}
			journal, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("journal retained", err)
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
