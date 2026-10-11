//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func appendLogCall(t *testing.T, f *appendLogFixture, r core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	profile := "plain"
	data, err := json.Marshal(frameRequest{Version: 1, CorrelationID: r.RequestID, Profile: profile, Request: f.credentials.token(t, r, encrypted)})
	if err != nil {
		t.Fatal(err)
	}
	response := appendLogDocument(t, f, r.RequestID, data)
	if response.CorrelationID != r.RequestID || response.Error != nil || response.Result == nil {
		t.Fatal("append-log RPC failure", response)
	}
	return response
}

func appendLogPublish(t *testing.T, f *appendLogFixture, name string, data []byte) {
	t.Helper()
	path := filepath.Join(f.directory, "logs", fmt.Sprintf("%020d.jsonl", f.generation))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(append(data, '\n'))
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(dir.Sync(), dir.Close()); err != nil {
		t.Fatal(err)
	}
}

func appendLogDocument(t *testing.T, f *appendLogFixture, name string, data []byte) adapter.FrameResponse {
	t.Helper()
	appendLogPublish(t, f, name, data)
	response := appendLogResult(t, f, name)
	if err := os.Remove(filepath.Join(f.directory, "response.json")); err != nil {
		t.Fatal(err)
	}
	return response
}

func appendLogResult(t *testing.T, f *appendLogFixture, name string) adapter.FrameResponse {
	t.Helper()
	var data []byte
	var err error
	output := filepath.Join(f.directory, "response.json")
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err = os.ReadFile(output)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) || !time.Now().Before(deadline) {
			t.Fatal("missing append-log result", err)
		}
		select {
		case <-f.process.done:
			t.Fatal("append-log service stopped", f.process.err, f.process.stderr.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(data, &response); err != nil || response.Version != 1 {
		t.Fatal("append-log RPC failure", err, response)
	}
	return response
}
