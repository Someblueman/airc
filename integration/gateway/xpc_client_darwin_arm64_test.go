//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func buildXPCBundle(t *testing.T, upstreamName string) (string, string) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "build-xpc-bridge.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	app := filepath.Join(root, "xpc-bridge.app")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, script, "--output", app, "--mach-service", upstreamName, "--sanitize")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build native XPC bridge: %v: %s", err, output)
	}
	client := filepath.Join(app, "Contents", "MacOS", "client")
	if info, err := os.Stat(client); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("XPC bridge client missing or not executable: %v (stat: %v)", client, err)
	}
	return app, client
}

func runXPCClient(ctx context.Context, client string, frame []byte, dropReply bool) ([]byte, error) {
	args := []string(nil)
	if dropReply {
		args = append(args, "--drop-reply")
	}
	command := exec.CommandContext(ctx, client, args...)
	command.Stdin = bytes.NewReader(frame)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("native XPC client: %w: %s", err, stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}
