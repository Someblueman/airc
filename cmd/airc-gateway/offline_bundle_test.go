//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestOfflineBundleCLIRefusesBeforeJournalAndReportsCapabilities(t *testing.T) {
	root := t.TempDir()
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "offline-cli", filepath.Join(root, "state")
	c.Listeners = []config.Listener{{ID: "offline", Protocol: "offline_bundle", Enabled: true, Operations: []core.Operation{core.Send}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "input.json")
	if err := os.WriteFile(input, []byte(`{"version":1,"requests":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"import-bundle"}, {"import-bundle", "--file", path},
		{"import-bundle", "--file", path, "--input", input, "extra"},
		{"import-bundle", "--file", path, "--input", input}, {"serve", "--file", path},
	} {
		var output bytes.Buffer
		if err := run(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("unsafe CLI accepted or emitted a report", args, err)
		}
		if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
			t.Fatal("refusal created journal state", err)
		}
	}
	var output bytes.Buffer
	if err := run([]string{"capabilities", "--file", path}, &output); err != nil || !bytes.Contains(output.Bytes(), []byte("enabled_offline_bundle_partial")) {
		t.Fatal("missing offline discovery", err)
	}
}
