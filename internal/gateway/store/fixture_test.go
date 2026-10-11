//go:build darwin || linux

package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// fixture is a journal configuration behind one upstream and two guest
// authors, "author" and "other". Guests hold no token, so their jobs stay
// runnable across a restart (a registered author's wait for its token).
func fixture(t *testing.T) (config.Config, Submission, Submission) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version = 1
	c.Realm = "fixture"
	c.StateDir = filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "irc.sock")}
	return c, fixtureAuthor(), guest("other", "fixture")
}

func request() core.Request {
	return core.Request{Version: 1, RequestID: "request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Message: "first\r\nsecond"}
}

// fixtureAuthor is the fixture's first author.
func fixtureAuthor() Submission { return guest("author", "fixture") }

// fixtureScope is the fixture author's scope.
func fixtureScope(config.Config) string {
	return core.Author{Nick: "author"}.Scope()
}

// scopeOf is the author scope a submission's jobs are keyed by.
func scopeOf(_ *Store, sub Submission) string {
	return sub.Author.Scope()
}
