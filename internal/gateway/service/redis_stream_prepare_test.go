//go:build darwin || linux

package service

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStreamsPreparationOwnsPasswordAndRouteSnapshot(t *testing.T) {
	c, cert, _, _ := natsServiceConfig(t)
	c.Listeners = []config.Listener{}
	password := filepath.Join(filepath.Dir(c.StateDir), "stream-password")
	original := []byte("password\r\n\x00")
	if err := os.WriteFile(password, original, 0600); err != nil {
		t.Fatal(err)
	}
	c.RedisStreams = []config.RedisStreams{{ID: "streams", Enabled: true, Namespace: "broker", Address: "127.0.0.1:6379", TLSServerName: "localhost", TLSCAFile: cert.CAPath,
		Credential: config.RedisCredential{Username: "reader", PasswordFile: password}, Group: "gateway", Consumer: "owner",
		Route: config.RedisStreamRoute{Principal: c.Principals[0].ID, Stream: "events", ResultStream: "results", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	owners, err := prepareRedisStreams(c, journal, v, b, nil)
	if err != nil || len(owners) != 1 {
		t.Fatal("preparation", err)
	}
	o := owners[0]
	defer o.clear()
	if !bytes.Equal(o.password, original) || o.security.MinVersion != tls.VersionTLS13 || o.security.InsecureSkipVerify || o.security.ServerName != "localhost" || o.security.RootCAs == nil || o.dispatcher == nil || o.sealer == nil {
		t.Fatal("incomplete preparation")
	}
	c.RedisStreams[0].Route.Operations[0] = core.Search
	if o.options.Route.Operations[0] != core.Send {
		t.Fatal("route aliases mutable config")
	}
	if err := os.WriteFile(password, []byte("rotated"), 0600); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(o.password, original) {
		t.Fatal("password is not a startup snapshot")
	}
	held := o.password
	o.clear()
	if !bytes.Equal(held, make([]byte, len(original))) {
		t.Fatal("password retained")
	}
	c.RedisStreams[0].Enabled = false
	skipped, err := prepareRedisStreams(c, journal, v, b, nil)
	if err != nil || len(skipped) != 0 {
		t.Fatal("disabled owner prepared", err)
	}
}
