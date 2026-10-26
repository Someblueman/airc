//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testwait"
)

func directoryMailboxCall(t *testing.T, f *directoryMailboxFixture, r core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	profile := "plain"
	data, err := json.Marshal(frameRequest{Version: 1, CorrelationID: r.RequestID, Profile: profile, Request: f.credentials.token(t, r, encrypted)})
	if err != nil {
		t.Fatal(err)
	}
	response := directoryMailboxDocument(t, f, r.RequestID, data)
	if response.CorrelationID != r.RequestID || response.Error != nil || response.Result == nil {
		t.Fatal("directory RPC failure", response)
	}
	return response
}

func directoryMailboxPublish(t *testing.T, f *directoryMailboxFixture, name string, data []byte) {
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
	inbox := filepath.Join(f.directory, "inbox")
	if err := os.Rename(temporary, filepath.Join(inbox, name+".json")); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(inbox)
	if err != nil {
		t.Fatal(err)
	}
	syncErr, closeErr = directory.Sync(), directory.Close()
	if syncErr != nil || closeErr != nil {
		t.Fatal(syncErr, closeErr)
	}
}

func directoryMailboxDocument(t *testing.T, f *directoryMailboxFixture, name string, data []byte) adapter.FrameResponse {
	t.Helper()
	directoryMailboxPublish(t, f, name, data)
	response := directoryMailboxResult(t, f, name)
	if err := os.Remove(filepath.Join(f.directory, "results", name+".json")); err != nil {
		t.Fatal(err)
	}
	return response
}

func directoryMailboxResult(t *testing.T, f *directoryMailboxFixture, name string) adapter.FrameResponse {
	t.Helper()
	var data []byte
	var err error
	output := filepath.Join(f.directory, "results", name+".json")
	testwait.Every(t, 5*time.Second, 10*time.Millisecond, "the directory result", func() bool {
		select {
		case <-f.process.done:
			t.Fatal("directory service stopped", f.process.err, f.process.stderr.String())
		default:
		}
		data, err = os.ReadFile(output)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal("missing directory result", err)
		}
		return err == nil
	})
	var response adapter.FrameResponse
	if err := json.Unmarshal(data, &response); err != nil || response.Version != 1 {
		t.Fatal("directory RPC failure", err, response)
	}
	return response
}
