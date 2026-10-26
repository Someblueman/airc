package gateway_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/store"
)

// acceptanceJournalFixture is a gateway configuration whose upstream is the
// aircd socket, and the registered author fixture-author with accountToken.
func acceptanceJournalFixture(t *testing.T, socket, accountToken string) (config.Config, store.Submission) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version = 1
	c.Realm = "wire-fixture"
	c.StateDir = filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: socket}
	return c, registeredSubmission(t, fixtureAuthor, accountToken)
}

// fixtureIdentity is the fixture's registered author, fixture-author.
func fixtureIdentity(t *testing.T, _ config.Config) store.Submission {
	t.Helper()
	return registeredSubmission(t, fixtureAuthor, fixtureAccount)
}
