package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestEnabledWatchRequiresCursorKeyAndWatchLimits(t *testing.T) {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", "/tmp/watch-state"
	c.Listeners = []Listener{{ID: "http", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Watch}}}
	if err := c.Validate(); err == nil {
		t.Fatal("watch enabled without cursor key")
	}
	c.CursorKeyFile = "/tmp/watch-key"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Limits.WatchesPerAuthor = 0
	if err := c.Validate(); err == nil {
		t.Fatal("watch enabled with disabled per-author limit")
	}
	c.Limits.WatchesPerAuthor, c.Limits.Watches = 0, 0
	if err := c.Validate(); err == nil {
		t.Fatal("watch enabled with disabled global limit")
	}
}
