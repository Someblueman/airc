//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestInheritedUnixCLIRefusalAndEOFJoinsJournal(t *testing.T) {
	binary := currentGatewayBinary(t)
	root := t.TempDir()
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "inherited-fixture", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "pair", Protocol: "inherited", Enabled: true, Operations: []core.Operation{core.Send}}}
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gateway.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	source, parent := inheritedNamedUnixFiles(t)
	defer source.Close()
	defer parent.Close()
	conn, err := net.FileConn(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	parent.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "serve-inherited", "--file", path)
	cmd.ExtraFiles = []*os.File{source}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	source.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("inherited-stream child did not join")
		}
	})
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "bad-signature", Profile: "plain", Request: "{}"})
	response := readFrame(t, conn)
	if response.CorrelationID != "bad-signature" || response.Error == nil || response.Error.Code != core.InvalidRequest {
		t.Fatal(response)
	}
	conn.Close()
	select {
	case err := <-done:
		done <- err
		if err != nil {
			t.Fatalf("clean EOF: %v %s", err, stderr.String())
		}
	case <-ctx.Done():
		t.Fatal("inherited-stream EOF did not join", ctx.Err())
	}
	if stdout.Len() != 0 {
		t.Fatal("inherited-stream command polluted stdout", stdout.String())
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal("journal ownership remained held", err)
	}
	pending, err := journal.HasPending(t.Context())
	closeErr := journal.Close()
	if pending || err != nil || closeErr != nil {
		t.Fatal("refusal admitted delivery", pending, err, closeErr)
	}
}

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
