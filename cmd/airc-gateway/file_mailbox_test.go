//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestFileMailboxCLIRefusalAndJoinedJournal(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "mailbox-cli", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "mailbox")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "files", Protocol: "file_mailbox", Address: directory, Enabled: true, Operations: []core.Operation{core.Send}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: "bad-signature", Profile: "signed", Envelope: "invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "request.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"mailbox-once", "--file", path}, &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("mailbox command emitted uncorrelated stdout")
	}
	data, err = os.ReadFile(filepath.Join(directory, "response.json"))
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(data, &response); err != nil || response.CorrelationID != "bad-signature" || response.Error == nil || response.Error.Code != core.AuthenticationFailed {
		t.Fatal("wrong CLI refusal", response, err)
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal("mailbox CLI did not join journal ownership", err)
	}
	pending, err := s.HasPending(t.Context())
	closeErr := s.Close()
	if pending || err != nil || closeErr != nil {
		t.Fatal("refusal admitted delivery", pending, err, closeErr)
	}
	if err := run([]string{"capabilities", "--file", path}, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("enabled_file_mailbox_partial")) {
		t.Fatal("file mailbox absent from discovery")
	}
}
