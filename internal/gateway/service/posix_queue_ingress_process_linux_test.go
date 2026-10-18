//go:build linux && (arm64 || amd64)

package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testmqueue"
)

func TestPOSIXIngressOwnerProcessHelper(t *testing.T) {
	path := os.Getenv("AGW_TEST_POSIX_OWNER_CONFIG")
	if path == "" {
		t.Skip("subprocess helper")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := openPOSIXIngress(t.Context(), journal, c.Listeners[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(queue.Identity()); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestPOSIXIngressRecoversKilledJournalOwner(t *testing.T) {
	testmqueue.Require(t)
	c, journal, l := posixIngressFixture(t)
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(c.StateDir), "owner-config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestPOSIXIngressOwnerProcessHelper$", "-test.timeout=30s")
	command.Env = append(os.Environ(), "AGW_TEST_POSIX_OWNER_CONFIG="+path)
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	var identity adapter.POSIXQueueIdentity
	ready := make(chan error, 1)
	go func() { ready <- json.NewDecoder(output).Decode(&identity) }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not become ready")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("owner was not killed")
	}
	journal, err = store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	record, err := journal.LoadPOSIXQueueOwnership(t.Context(), l.ID)
	if err != nil || record.QueueDevice != identity.Device || record.QueueInode != identity.Inode {
		t.Fatal("killed owner's durable record lost", record, err)
	}
	recovered, err := openPOSIXIngress(t.Context(), journal, l)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Identity() != identity {
		t.Fatal("original queue was not recovered")
	}
	if err := recovered.WriteMessage(t.Context(), []byte("retained")); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, adapter.POSIXQueueMessageBytes)
	n, _, err := recovered.ReadMessage(t.Context(), body)
	if err != nil || string(body[:n]) != "retained" {
		t.Fatal("recovered I/O", err)
	}
}
