//go:build darwin || linux

package gateway_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

type fileMailboxFixture struct {
	config                  config.Config
	credentials             *detachedCredentials
	observer                *irc.Client
	binary, path, directory string
}

func newFileMailboxFixture(t *testing.T) *fileMailboxFixture {
	t.Helper()
	binary := currentGatewayBinary(t)
	native := fixtureWithAdmin(t, true)
	provision := dial(t, native, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, native)
	c, _ := acceptanceJournalFixture(t, native, fixtureAccount)
	root, err := filepath.EvalSymlinks(filepath.Dir(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := []core.Operation{core.Send, core.ReceiptGet, core.HistoryRead}
	credentials := provisionDetached(t, &c)
	directory := filepath.Join(root, "mailbox")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "files", Protocol: "file_mailbox", Address: directory, Enabled: true, Operations: ops}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gateway.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	observer := dial(t, native, "file-mailbox-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "file-mailbox-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return &fileMailboxFixture{config: c, credentials: credentials, observer: observer, binary: binary, path: path, directory: directory}
}

func fileMailboxCall(t *testing.T, f *fileMailboxFixture, r core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	data, err := json.Marshal(frameRequest{Version: 1, CorrelationID: r.RequestID, Profile: "plain", Request: f.credentials.token(t, r, encrypted)})
	if err != nil {
		t.Fatal(err)
	}
	response := fileMailboxDocument(t, f, data)
	if response.CorrelationID != r.RequestID || response.Error != nil || response.Result == nil {
		t.Fatal("file mailbox RPC failure", response)
	}
	return response
}

func noMailboxJob(t *testing.T, f *fileMailboxFixture, ids ...string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(f.config.StateDir, "journal.sqlite"), RawQuery: "mode=ro&_pragma=busy_timeout(3000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	for _, id := range ids {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE request_id=?", id).Scan(&count); err != nil || count != 0 {
			t.Fatal("refused mailbox request reached intake", id, count, err)
		}
	}
}
