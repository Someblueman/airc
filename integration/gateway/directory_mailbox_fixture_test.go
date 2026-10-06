//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type directoryMailboxFixture struct {
	*fileMailboxFixture
	process *gatewayProcess
}

func newDirectoryMailboxFixture(t *testing.T) *directoryMailboxFixture {
	t.Helper()
	f := newFileMailboxFixture(t)
	for _, name := range []string{"inbox", "processing", "results"} {
		if err := os.Mkdir(filepath.Join(f.directory, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.config.Listeners[0].Protocol = "directory_mailbox"
	f.config.Listeners[0].DirectoryMailbox = &config.DirectoryMailbox{Principal: f.config.Principals[0].ID}
	d := &directoryMailboxFixture{fileMailboxFixture: f}
	d.start(t)
	return d
}

func (f *directoryMailboxFixture) start(t *testing.T) {
	t.Helper()
	f.process = startGateway(t, f.binary, f.config)
	if address := gatewayAddress(t, f.process); address != f.directory {
		t.Fatal("wrong directory readiness", address)
	}
}

func (f *directoryMailboxFixture) stop(t *testing.T) {
	t.Helper()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal("directory shutdown", f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("directory shutdown did not join")
	}
}

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
	if err := os.Rename(temporary, filepath.Join(inbox, r.RequestID+".json")); err != nil {
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
	output := filepath.Join(f.directory, "results", r.RequestID+".json")
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
	if err := json.Unmarshal(data, &response); err != nil || response.Version != 1 || response.CorrelationID != r.RequestID || response.Error != nil || response.Result == nil {
		t.Fatal("directory RPC failure", err, response)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	return response
}
