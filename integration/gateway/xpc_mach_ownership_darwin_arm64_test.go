//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildXPCMachOwnershipProbe(t *testing.T) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "xpc-mach-ownership")
	source, err := filepath.Abs(filepath.Join("testdata", "xpc_mach_ownership.c"))
	if err != nil {
		t.Fatal(err)
	}
	nativeSource, err := filepath.Abs(filepath.Join("..", "..", "native", "xpc", "mach.c"))
	if err != nil {
		t.Fatal(err)
	}
	includeDir, err := filepath.Abs(filepath.Join("..", "..", "native", "xpc"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "xcrun", "--sdk", "macosx", "clang", "-std=c11", "-fblocks",
		"-Wall", "-Wextra", "-Werror", "-pedantic", "-target", "arm64-apple-macos13.0",
		"-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-fno-omit-frame-pointer",
		"-g", "-I"+includeDir,
		source, nativeSource, "-framework", "CoreFoundation", "-o", output)
	if diagnostic, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Mach failed-send ownership probe: %v: %s", err, diagnostic)
	}
	if info, err := os.Stat(output); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("Mach ownership probe is not executable: info=%v err=%v", info, err)
	}
	return output
}

func TestXPCMachFailedSendReleasesPseudoReceivedRights(t *testing.T) {
	probe := buildXPCMachOwnershipProbe(t)
	name := fmt.Sprintf("org.airc.xpc.ownership.%d.%d", os.Getpid(), time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, probe, name)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("Mach failed-send ownership probe: %v: %s%s", err, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "timeout; service refs and port-name set unchanged" {
		t.Fatalf("Mach ownership probe output=%q", got)
	}
}
