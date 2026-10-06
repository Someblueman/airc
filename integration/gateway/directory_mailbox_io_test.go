//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func directoryMailboxCall(t *testing.T, f *directoryMailboxFixture, r core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	profile := "signed"
	if encrypted {
		profile = "encrypted"
	}
	data, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: r.RequestID, Profile: profile, Envelope: f.credentials.token(t, r, encrypted)})
	if err != nil {
		t.Fatal(err)
	}
	response := directoryMailboxDocument(t, f, r.RequestID, data)
	if response.CorrelationID != r.RequestID || response.Error != nil || response.Result == nil {
		t.Fatal("directory RPC failure", response)
	}
	return response
}

func directoryMailboxDocument(t *testing.T, f *directoryMailboxFixture, name string, data []byte) adapter.FrameResponse {
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
	output := filepath.Join(f.directory, "results", name+".json")
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err = os.ReadFile(output)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) || !time.Now().Before(deadline) {
			t.Fatal("missing directory result", err)
		}
		select {
		case <-f.process.done:
			t.Fatal("directory service stopped", f.process.err, f.process.stderr.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(data, &response); err != nil || response.Version != 1 {
		t.Fatal("directory RPC failure", err, response)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	return response
}
