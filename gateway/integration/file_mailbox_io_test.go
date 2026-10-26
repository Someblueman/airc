//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"github.com/Someblueman/airc/gateway/internal/adapter"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func fileMailboxPublish(t *testing.T, f *fileMailboxFixture, data []byte) {
	t.Helper()
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
}

func fileMailboxCommand(t *testing.T, f *fileMailboxFixture) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	t.Cleanup(cancel)
	return exec.CommandContext(ctx, f.binary, "mailbox-once", "--file", f.path)
}

func fileMailboxDocument(t *testing.T, f *fileMailboxFixture, data []byte) adapter.FrameResponse {
	t.Helper()
	fileMailboxPublish(t, f, data)
	command := fileMailboxCommand(t, f)
	if diagnostic, err := command.CombinedOutput(); err != nil || len(diagnostic) != 0 {
		t.Fatalf("file mailbox command: %v %s", err, diagnostic)
	}
	output := filepath.Join(f.directory, "response.json")
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(data, &response); err != nil || response.Version != 1 {
		t.Fatal("file mailbox RPC failure", err, response)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	return response
}
