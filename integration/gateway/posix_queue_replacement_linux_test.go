//go:build linux && (arm64 || amd64)

package gateway_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"golang.org/x/sys/unix"

	"github.com/Someblueman/airc/internal/testmqueue"
)

func TestPOSIXQueueReplacementIsPreservedAndNeverAdopted(t *testing.T) {
	testmqueue.Require(t)
	f := newPOSIXFixtureShutdown(t, false)
	original := f.source.Identity()
	// Keep the original descriptor open while replacing the kernel name. This
	// guarantees a distinct live inode rather than allowing inode-number reuse.
	if err := unix.Unlink("/dev/mqueue" + f.address); err != nil {
		t.Fatal(err)
	}
	replacement, err := adapter.CreatePOSIXQueue(f.address, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.Identity() == original {
		t.Fatal("replacement reused a live original inode")
	}
	if err := f.source.WriteMessage(t.Context(), []byte("never send through replaced name")); !errors.Is(err, adapter.ErrPOSIXQueueInvalid) {
		t.Fatal("old producer handle ignored replacement", err)
	}
	select {
	case <-f.process.done:
		if f.process.err == nil {
			t.Fatal("replaced ingress exited without reporting failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replaced ingress did not join")
	}
	opened, err := adapter.OpenPOSIXQueue(f.address, 8)
	if err != nil {
		t.Fatal("shutdown removed external replacement", err)
	}
	defer opened.Close()
	if opened.Identity() != replacement.Identity() {
		t.Fatal("shutdown changed replacement")
	}
	// A journal record for the original inode cannot authorize this replacement.
	p := startGateway(t, gatewayBinary(t), f.config)
	select {
	case <-p.done:
		if p.err == nil {
			t.Fatal("startup adopted replacement")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replacement startup did not refuse")
	}
	select {
	case ready := <-p.ready:
		t.Fatal("refused startup reported ready", string(ready))
	default:
	}
	again, err := adapter.OpenPOSIXQueue(f.address, 8)
	if err != nil {
		t.Fatal("refusal removed replacement", err)
	}
	defer again.Close()
	if again.Identity() != replacement.Identity() {
		t.Fatal("refusal changed replacement")
	}
	if _, err := os.Stat("/dev/mqueue" + f.mailbox.Name()); err != nil {
		t.Fatal("response queue removed", err)
	}
	rpcNoJobs(t, f.config.StateDir)
	if records := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(records) != 0 {
		t.Fatal("replacement admitted native input", records)
	}
}
