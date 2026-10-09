//go:build (darwin || linux) && arm64

package gateway_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"golang.org/x/sys/unix"
)

const sharedMemoryCrashChildEnv = "AIRC_SHARED_MEMORY_CRASH_CHILD"

func TestSharedMemoryCrashChild(t *testing.T) {
	mode := os.Getenv(sharedMemoryCrashChildEnv)
	if mode == "" {
		return
	}
	name, directory := os.Getenv("AIRC_SHARED_MEMORY_NAME"), os.Getenv("AIRC_SHARED_MEMORY_DIRECTORY")
	if !validSharedMemoryFixtureName(name) || !filepath.IsAbs(directory) {
		t.Fatal("invalid child shared-memory fixture arguments")
	}
	if err := runSharedMemoryCrashWriter(name, directory, mode, os.Stdout); err != nil {
		t.Fatal(err)
	}
	select {}
}

func TestSharedMemoryQueueRecoversAfterWriterCrash(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		mode  string
		stage string
		want  []byte
	}{
		{mode: "writing-partial", stage: "writing-partial"},
		{mode: "ready", stage: "ready", want: []byte("complete record published before writer death")},
		{mode: "writing-reset", stage: "writing-reset"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			name, directory := provisionSharedMemory(t, root)
			child := startSharedMemoryCrashChild(t, name, directory, test.mode)
			if stage := child.waitStage(t); stage != test.stage {
				t.Fatalf("child stage=%q want %q", stage, test.stage)
			}
			assertSharedMemoryCrashLockHeld(t, directory)
			child.killAndWait(t)

			consumer, err := adapter.OpenSharedMemoryQueue(name, directory, true)
			if err != nil {
				t.Fatalf("transaction flock was not released after SIGKILL: %v", err)
			}
			defer consumer.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 120*time.Millisecond)
			got := make([]byte, 1000)
			n, err := consumer.ReadMessage(ctx, got)
			cancel()
			if len(test.want) != 0 {
				if err != nil || n != len(test.want) || !bytes.Equal(got[:n], test.want) {
					t.Fatalf("READY record was not retained: n=%d err=%v body=%q", n, err, got[:n])
				}
				return
			}
			if n != 0 || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("incomplete record was not discarded to EMPTY: n=%d err=%v", n, err)
			}
			producer, err := adapter.OpenSharedMemoryQueue(name, directory, false)
			if err != nil {
				t.Fatal(err)
			}
			defer producer.Close()
			want := []byte("fresh record after crash recovery")
			writeCtx, writeCancel := context.WithTimeout(t.Context(), time.Second)
			defer writeCancel()
			if err := producer.WriteMessage(writeCtx, want); err != nil {
				t.Fatalf("recovered slot did not accept a new message: %v", err)
			}
			n, err = consumer.ReadMessage(writeCtx, got)
			if err != nil || n != len(want) || !bytes.Equal(got[:n], want) {
				t.Fatalf("post-recovery exchange n=%d err=%v body=%q", n, err, got[:n])
			}
		})
	}
}

func assertSharedMemoryCrashLockHeld(t *testing.T, directory string) {
	t.Helper()
	lock, err := os.OpenFile(filepath.Join(directory, "transaction.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	closeErr := lock.Close()
	if err == nil {
		t.Fatal("child emitted stage witness without holding transaction flock")
	}
	if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("checking child transaction flock: %v", err)
	}
	if closeErr != nil {
		t.Errorf("close transaction lock probe: %v", closeErr)
	}
}

func sharedMemoryCrashEnvironment(name, directory, mode string) []string {
	keys := []string{sharedMemoryCrashChildEnv, "AIRC_SHARED_MEMORY_NAME", "AIRC_SHARED_MEMORY_DIRECTORY"}
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		keep := true
		for _, key := range keys {
			if strings.HasPrefix(entry, key+"=") {
				keep = false
				break
			}
		}
		if keep {
			env = append(env, entry)
		}
	}
	return append(env, sharedMemoryCrashChildEnv+"="+mode, "AIRC_SHARED_MEMORY_NAME="+name, "AIRC_SHARED_MEMORY_DIRECTORY="+directory)
}
