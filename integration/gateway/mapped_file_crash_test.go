//go:build (darwin || linux) && arm64

package gateway_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
)

const mappedFileCrashChildEnv = "AIRC_MAPPED_FILE_CRASH_CHILD"

func TestMappedFileCrashChild(t *testing.T) {
	mode := os.Getenv(mappedFileCrashChildEnv)
	if mode == "" {
		return
	}
	directory := os.Getenv("AIRC_MAPPED_FILE_CRASH_DIRECTORY")
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		t.Fatal("invalid mapped-file crash fixture directory")
	}
	if err := runMappedFileCrashWriter(directory, mode, os.Stdout); err != nil {
		t.Fatal(err)
	}
	t.Fatal("mapped-file crash writer unexpectedly returned")
}

func TestMappedFileQueueRecoversAfterLastMapperWriterCrash(t *testing.T) {
	root := mappedFileFixtureRoot(t)
	for _, test := range []struct {
		mode  string
		stage string
		want  []byte
	}{
		{mode: "writing-partial", stage: "writing-partial"},
		{mode: "ready", stage: "ready", want: []byte("complete mapped-file record before writer death")},
		{mode: "writing-reset", stage: "writing-reset"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			directory := provisionMappedFileQueue(t, root)
			queuePath := filepath.Join(directory, "queue.dat")
			before, err := os.Stat(queuePath)
			if err != nil {
				t.Fatal(err)
			}
			child := startMappedFileCrashChild(t, directory, test.mode)
			if got := child.waitStage(t); got != test.stage {
				t.Fatalf("child stage=%q want %q", got, test.stage)
			}
			assertSharedMemoryCrashLockHeld(t, directory)
			child.killAndWait(t)
			after, err := os.Stat(queuePath)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("last mapper death replaced the ordinary queue file: before=%v after=%v err=%v", before, after, err)
			}

			consumer, err := adapter.OpenMappedFileQueue(directory, true)
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
			producer, err := adapter.OpenMappedFileQueue(directory, false)
			if err != nil {
				t.Fatal(err)
			}
			defer producer.Close()
			want := []byte("fresh record after last-mapper crash recovery")
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

func startMappedFileCrashChild(t *testing.T, directory, mode string) *sharedMemoryCrashChild {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestMappedFileCrashChild$", "-test.timeout=15s")
	command.Env = mappedFileCrashEnvironment(directory, mode)
	return startQueueCrashProcess(t, command)
}

func mappedFileCrashEnvironment(directory, mode string) []string {
	keys := []string{mappedFileCrashChildEnv, "AIRC_MAPPED_FILE_CRASH_DIRECTORY"}
	env := make([]string, 0, len(os.Environ())+2)
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
	return append(env, mappedFileCrashChildEnv+"="+mode, "AIRC_MAPPED_FILE_CRASH_DIRECTORY="+directory)
}
