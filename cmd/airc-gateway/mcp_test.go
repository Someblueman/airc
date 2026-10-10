package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPHTTPConfigurationDiscoveryDoesNotStartRuntime(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	path := filepath.Join(root, "config.json")
	data := `{"version":1,"realm":"fixture","state_dir":"` + state + `","listeners":[{"id":"mcp","protocol":"mcp_http","enabled":true,"operations":["send","receipt.get"]}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"capabilities", "--file", path}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"status":"enabled_mcp_http_partial"`) || !strings.Contains(out.String(), `"operations":["send","receipt.get"]`) || strings.Contains(out.String(), root) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("MCP discovery created runtime state", err)
	}
}
