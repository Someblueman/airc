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

func buildMachIPCClient(t *testing.T) string {
	t.Helper()
	compiler, err := exec.LookPath("clang")
	if err != nil {
		t.Fatalf("clang required to build native Mach IPC acceptance client: %v", err)
	}
	output := filepath.Join(t.TempDir(), "mach-ipc-client")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, compiler, "-std=c11", "-Wall", "-Wextra", "-Werror", "-pedantic",
		"-fsanitize=address,undefined", "-fno-omit-frame-pointer", "-g",
		filepath.Join("testdata", "mach_ipc_client.c"), "-o", output)
	if diagnostic, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build native Mach IPC client: %v: %s", err, diagnostic)
	}
	if info, err := os.Stat(output); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("built Mach IPC client is not executable: info=%v err=%v", info, err)
	}
	return output
}

func runMachIPCClient(ctx context.Context, binary, bootstrapName string, request []byte, options ...string) ([]byte, error) {
	arguments := append([]string{bootstrapName}, options...)
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Stdin = bytes.NewReader(request)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("native Mach IPC client: %w: %s", err, stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func TestMachIPCClientBuildsWithSanitizers(t *testing.T) {
	_ = buildMachIPCClient(t)
}
