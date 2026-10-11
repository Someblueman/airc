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

func TestInheritedUnixStartupRejectsBeforeJournal(t *testing.T) {
	binary := currentGatewayBinary(t)
	for _, name := range []string{"missing-descriptor", "regular-file", "ordinary-serve"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			c := config.Defaults()
			c.Version, c.Realm, c.StateDir = 1, "inherited-startup", filepath.Join(root, "absent-state")
			c.Listeners = []config.Listener{{ID: "pair", Protocol: "inherited", Enabled: true, Operations: []core.Operation{core.Send}}}
			body, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "gateway.json")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := "serve-inherited"
			if name == "ordinary-serve" {
				command = "serve"
			}
			cmd := exec.CommandContext(ctx, binary, command, "--file", path)
			file, err := os.CreateTemp(root, "regular")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if name == "regular-file" {
				cmd.ExtraFiles = []*os.File{file}
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err == nil || ctx.Err() != nil {
				t.Fatal("startup refusal", err, ctx.Err())
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatal("invalid startup diagnostics", stdout.String(), stderr.String())
			}
			if _, err := os.Lstat(c.StateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refusal touched journal", err)
			}
		})
	}
}
