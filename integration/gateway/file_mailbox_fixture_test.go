//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
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
	c.Principals[0].Operations = ops
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
	return &fileMailboxFixture{config: c, credentials: credentials, observer: observer, binary: currentGatewayBinary(t), path: path, directory: directory}
}

func fileMailboxCall(t *testing.T, f *fileMailboxFixture, r core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	profile := "signed"
	if encrypted {
		profile = "encrypted"
	}
	data, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: r.RequestID, Profile: profile, Envelope: f.credentials.token(t, r, encrypted)})
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(f.directory, "producer.tmp")
	fd, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := fd.Write(data)
	syncErr, closeErr := fd.Sync(), fd.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		t.Fatal(writeErr, syncErr, closeErr)
	}
	if err := os.Rename(temporary, filepath.Join(f.directory, "request.json")); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(f.directory)
	if err != nil {
		t.Fatal(err)
	}
	syncErr, closeErr = directory.Sync(), directory.Close()
	if syncErr != nil || closeErr != nil {
		t.Fatal(syncErr, closeErr)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, f.binary, "mailbox-once", "--file", f.path)
	if diagnostic, err := command.CombinedOutput(); err != nil || len(diagnostic) != 0 {
		t.Fatalf("file mailbox command: %v %s", err, diagnostic)
	}
	output := filepath.Join(f.directory, "response.json")
	data, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(data, &response); err != nil || response.Version != 1 || response.CorrelationID != r.RequestID || response.Error != nil || response.Result == nil {
		t.Fatal("file mailbox RPC failure", err, response)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	return response
}
