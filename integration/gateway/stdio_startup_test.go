//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestStdioStartupRejectsBeforeJournal(t *testing.T) {
	binary := gatewayBinary(t)
	for _, name := range []string{"ordinary-serve", "file-input", "file-output"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			c := config.Defaults()
			c.Version, c.Realm, c.StateDir = 1, "stdio-startup", filepath.Join(root, "absent-state")
			c.Listeners = []config.Listener{{ID: "stdio", Protocol: "stdio", Enabled: true, Operations: []core.Operation{core.Send}}}
			body, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "config.json")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := "serve-stdio"
			if name == "ordinary-serve" {
				command = "serve"
			}
			cmd := exec.CommandContext(ctx, binary, command, "--file", path)
			cmd.Stdin = bytes.NewReader(nil)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			file, err := os.CreateTemp(root, "regular-file")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if name == "file-input" {
				cmd.Stdin = file
			}
			if name == "file-output" {
				cmd.Stdout = file
			}
			if err := cmd.Run(); err == nil || ctx.Err() != nil {
				t.Fatalf("startup rejection: %v, context %v", err, ctx.Err())
			}
			if stdout.Len() != 0 {
				t.Fatalf("rejected startup polluted stdout: %q", stdout.String())
			}
			stat, err := file.Stat()
			if err != nil || stat.Size() != 0 {
				t.Fatalf("rejected startup wrote file: %v %v", stat, err)
			}
			if _, err := os.Lstat(c.StateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected startup touched journal: %v", err)
			}
			if stderr.Len() == 0 {
				t.Fatal("missing stderr diagnostic")
			}
		})
	}
}
