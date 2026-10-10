//go:build darwin && arm64

package gateway_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func provisionSharedMemoryObject(t *testing.T, name string) (func() error, error) {
	t.Helper()
	if !validSharedMemoryFixtureName(name) {
		return nil, fmt.Errorf("invalid shared-memory fixture name %q", name)
	}
	compiler, err := exec.LookPath("clang")
	if err != nil {
		return nil, fmt.Errorf("clang required to build Darwin shm fixture helper: %w", err)
	}
	root, err := os.MkdirTemp("", "airc-shm-helper-")
	if err != nil {
		return nil, fmt.Errorf("create private helper directory: %w", err)
	}
	helper := filepath.Join(root, "shm-fixture")
	source := filepath.Join("testdata", "shared_memory_fixture.c")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "-std=c11", "-Wall", "-Wextra", "-Werror", "-pedantic", source, "-o", helper)
	if output, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("build Darwin shm fixture helper: %w: %s", err, output)
	}
	if err := runSharedMemoryHelper(ctx, helper, "create", name); err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("create Darwin shm fixture object: %w", err)
	}
	return func() error {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		return errors.Join(
			runSharedMemoryHelper(cleanupCtx, helper, "unlink", name),
			os.RemoveAll(root),
		)
	}, nil
}

func runSharedMemoryHelper(ctx context.Context, helper, operation, name string) error {
	command := exec.CommandContext(ctx, helper, operation, name)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", operation, name, err, output)
	}
	return nil
}
