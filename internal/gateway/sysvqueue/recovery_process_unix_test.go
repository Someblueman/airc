//go:build (darwin && arm64) || (linux && (amd64 || arm64))

package sysvqueue

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/ipcepoch"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

type recoveryRecord struct {
	Identity Identity
	Epoch    ipcepoch.Epoch
}

func TestSystemVRecoveryChild(t *testing.T) {
	key := os.Getenv("AIRC_SYSV_RECOVERY_KEY")
	if key == "" {
		t.Skip("owned child only")
	}
	parsed, err := strconv.ParseInt(key, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	q, err := Create(int32(parsed))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	epoch, err := ipcepoch.Current()
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 1000)
	for i := range body {
		body[i] = byte(i)
	}
	if err := q.WriteMessage(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(recoveryRecord{q.Identity(), epoch}); err != nil {
		t.Fatal(err)
	}
	<-t.Context().Done() // Parent kills this real queue owner before deferred cleanup.
}
func TestSystemVRecoveryPreservesPacketAfterKilledOwner(t *testing.T) {
	placeholder := fixture(t)
	key := placeholder.Identity().Key
	if err := placeholder.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestSystemVRecoveryChild$", "-test.timeout=30s")
	child.Env = append(os.Environ(), "AIRC_SYSV_RECOVERY_KEY="+strconv.FormatInt(int64(key), 10))
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	var record recoveryRecord
	ready := make(chan error, 1)
	go func() { ready <- json.NewDecoder(stdout).Decode(&record) }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("queue owner did not become ready", ctx.Err())
	}
	t.Cleanup(func() {
		q, err := Resume(record.Identity, record.Epoch)
		if errors.Is(err, unix.ENOENT) {
			return
		}
		if err != nil {
			t.Error(err)
			return
		}
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	waited = true
	if err == nil {
		t.Fatal("queue owner did not die")
	}
	recovered, err := Resume(record.Identity, record.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 1000)
	n, err := recovered.ReadMessage(t.Context(), body)
	if err != nil || n != 1000 {
		t.Fatal("lost queued packet", n, err)
	}
	for i, b := range body {
		if b != byte(i) {
			t.Fatal("packet changed", i)
		}
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(key); !errors.Is(err, unix.ENOENT) {
		t.Fatal("recovered queue remains", err)
	}
}
