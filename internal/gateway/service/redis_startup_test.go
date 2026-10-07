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

func TestRedisStartupFailureNeverClaimsReadyOrRetainsJournal(t *testing.T) {
	for _, kind := range []string{"password", "trust", "response_signer"} {
		t.Run(kind, func(t *testing.T) {
			c, cert, _, _ := natsServiceConfig(t)
			c.Listeners, c.Limits.Sessions = []config.Listener{}, 2
			root := filepath.Dir(c.StateDir)
			password := filepath.Join(root, "redis-password")
			if err := os.WriteFile(password, []byte("password"), 0600); err != nil {
				t.Fatal(err)
			}
			publisher := filepath.Join(root, "redis-publisher-password")
			if err := os.WriteFile(publisher, []byte("password"), 0600); err != nil {
				t.Fatal(err)
			}
			c.RedisPubSub = []config.RedisPubSub{{ID: "redis", Enabled: true, Address: "127.0.0.1:6379", TLSServerName: "localhost", TLSCAFile: cert.CAPath,
				Subscriber: config.RedisCredential{Username: "reader", PasswordFile: password}, Publisher: config.RedisCredential{Username: "writer", PasswordFile: publisher},
				Routes: []config.RedisRoute{{Principal: c.Principals[0].ID, RequestChannel: "request", ResponseChannel: "response", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}}
			path := password
			content := []byte{}
			if kind == "trust" {
				path, content = cert.CAPath, []byte("invalid CA")
			}
			if kind == "response_signer" {
				path, content = c.ResponseSigningKey.PrivateKeyFile, []byte("invalid signer")
			}
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := c.CheckFiles(); err != nil {
				t.Fatal("fixture file references", err)
			}
			if err := Run(t.Context(), c, &output); err == nil || output.Len() != 0 {
				t.Fatal("failed preparation claimed ready", err)
			}
			journal, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("failed startup retained journal", err)
			}
			_ = journal.Close()
		})
	}
}
