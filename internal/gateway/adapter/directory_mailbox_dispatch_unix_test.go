//go:build darwin || linux

package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestDirectoryMailboxRefusesForeignCorrelation: a frame's correlation ID
// must name its own file; TestFrameCarriers covers the shared frame refusals.
func TestDirectoryMailboxRefusesForeignCorrelation(t *testing.T) {
	f, body := frameFixture(t)
	body = bytes.Replace(body, []byte(`"correlation_id":"native-rpc"`), []byte(`"correlation_id":"other"`), 1)
	path, m := directoryMailboxFixture(t)
	if err := os.WriteFile(filepath.Join(path, "inbox", "native-rpc.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ProcessDirectoryMailbox(t.Context(), m, f.d, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(path, "results", "native-rpc.json"))
	var response FrameResponse
	if err != nil || json.Unmarshal(data, &response) != nil || response.Error == nil || response.Error.Code != core.InvalidRequest {
		t.Fatal("wrong directory refusal", err, response)
	}
	if _, err := f.lookup(t, "frame-message"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("directory refusal reached durable intake", err)
	}
}
