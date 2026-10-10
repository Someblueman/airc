//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/service"
)

func newOfflineBundleFixture(t *testing.T) *fileMailboxFixture {
	t.Helper()
	f := newFileMailboxFixture(t)
	f.config.Listeners = []config.Listener{{ID: "offline", Protocol: "offline_bundle", Enabled: true, Operations: []core.Operation{core.Send}}}
	data, err := json.Marshal(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return f
}

func offlineBundleCall(t *testing.T, f *fileMailboxFixture, tokens []string, success bool) service.OfflineImportReport {
	t.Helper()
	data, err := json.Marshal(adapter.OfflineBundle{Version: 1, Envelopes: tokens})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(f.directory, "bundle.json")
	if err := os.WriteFile(input, data, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, f.binary, "import-bundle", "--file", f.path, "--input", input)
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	output, err := command.Output()
	if (err == nil) != success || ctx.Err() != nil {
		t.Fatalf("offline command success=%v error=%v diagnostics=%s", success, err, diagnostics.Bytes())
	}
	after, readErr := os.ReadFile(input)
	if readErr != nil || !bytes.Equal(data, after) {
		t.Fatal("import modified its source", readErr)
	}
	var report service.OfflineImportReport
	if len(output) > 0 && (json.Unmarshal(output, &report) != nil || report.Version != 1) {
		t.Fatal("invalid offline report")
	}
	return report
}
