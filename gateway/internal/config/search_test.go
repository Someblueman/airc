package config

import (
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestEnabledSearchRequiresCursorKeyAndSendReserve(t *testing.T) {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", "/tmp/search-state"
	c.Listeners = []Listener{{ID: "http", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Search}}}
	if err := c.Validate(); err == nil {
		t.Fatal("search enabled without cursor key")
	}
	c.CursorKeyFile = "/tmp/search-key"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Limits.SessionsPerAuthor = 1
	if err := c.Validate(); err == nil {
		t.Fatal("search enabled without per-author send reserve")
	}
}
