//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/auth"
)

func TestCredentialCLI(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gateway-token")
	var out bytes.Buffer
	if err := run([]string{"credential", "init", "--file", path}, &out); err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "credential created") || strings.Contains(out.String(), token) {
		t.Fatal("missing success or secret reflected")
	}
	if err := run([]string{"credential", "init", "--file", path}, &out); err == nil {
		t.Fatal("credential CLI overwrote file")
	}
	if err := run([]string{"credential", "init", "--file", path, "stray"}, &out); err == nil {
		t.Fatal("accepted stray provisioning argument")
	}
}
