//go:build linux && (arm64 || amd64)

package adapter

import (
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testmqueue"
)

type queueRecoveryRecord struct {
	Identity POSIXQueueIdentity
	Epoch    POSIXQueueEpoch
}

func TestPOSIXQueueRecoveryHelper(t *testing.T) {
	name := os.Getenv("AGW_TEST_QUEUE_RECOVERY")
	if name == "" {
		t.Skip("subprocess helper")
	}
	queue, err := CreatePOSIXQueue(name, 1)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := CurrentPOSIXQueueEpoch()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(queueRecoveryRecord{queue.Identity(), epoch}); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	} // Parent kills the actual queue owner before cleanup.
}

func TestPOSIXQueueRecoveryAfterKilledOwner(t *testing.T) {
	testmqueue.Require(t)
	placeholder := newTestPOSIXQueue(t, 1)
	name := placeholder.Name()
	if err := placeholder.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestPOSIXQueueRecoveryHelper$", "-test.timeout=30s")
	command.Env = append(os.Environ(), "AGW_TEST_QUEUE_RECOVERY="+name)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	var record queueRecoveryRecord
	ready := make(chan error, 1)
	go func() { ready <- json.NewDecoder(stdout).Decode(&record) }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queue owner did not become ready")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("owner did not die")
	}
	t.Cleanup(func() {
		q, err := ResumePOSIXQueue(name, 1, record.Identity, record.Epoch)
		if err == nil {
			_ = q.Close()
		}
	})
	wrongEpoch := record.Epoch
	wrongEpoch.BootID = "00000000-0000-0000-0000-000000000000"
	wrongNamespace := record.Epoch
	wrongNamespace.NamespaceInode++
	wrongIdentity := record.Identity
	wrongIdentity.Inode++
	for _, bad := range []queueRecoveryRecord{{record.Identity, wrongEpoch}, {record.Identity, wrongNamespace}, {wrongIdentity, record.Epoch}} {
		if q, err := ResumePOSIXQueue(name, 1, bad.Identity, bad.Epoch); q != nil || !errors.Is(err, ErrPOSIXQueueInvalid) {
			t.Fatalf("adopted stale queue: %v %v", q, err)
		}
		retained, err := OpenPOSIXQueue(name, 1)
		if err != nil {
			t.Fatal("refusal removed name", err)
		}
		_ = retained.Close()
	}
	recovered, err := ResumePOSIXQueue(name, 1, record.Identity, record.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.WriteMessage(t.Context(), []byte("recovered")); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, POSIXQueueMessageBytes)
	n, _, err := recovered.ReadMessage(t.Context(), body)
	if err != nil || string(body[:n]) != "recovered" {
		t.Fatal("recovered I/O", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPOSIXQueue(name, 1); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovered owner failed cleanup", err)
	}
}

func TestPOSIXQueueRecoveryPreservesReplacement(t *testing.T) {
	testmqueue.Require(t)
	original := newTestPOSIXQueue(t, 1)
	epoch, err := CurrentPOSIXQueueEpoch()
	if err != nil {
		t.Fatal(err)
	}
	identity := original.Identity()
	if err := unix.Unlinkat(original.directory, original.Name()[1:], 0); err != nil {
		t.Fatal(err)
	}
	replacement, err := CreatePOSIXQueue(original.Name(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.Identity() == identity {
		t.Fatal("live original inode reused")
	}
	recovered, err := ResumePOSIXQueue(original.Name(), 1, identity, epoch)
	if recovered != nil || !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatalf("adopted replacement: %v %v", recovered, err)
	}
	retained, err := OpenPOSIXQueue(replacement.Name(), 1)
	if err != nil {
		t.Fatal("replacement removed", err)
	}
	if retained.Identity() != replacement.Identity() {
		t.Fatal("replacement identity changed")
	}
	_ = retained.Close()
}
