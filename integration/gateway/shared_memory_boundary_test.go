//go:build (darwin || linux) && arm64

package gateway_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSharedMemoryQueueNativeOwnershipFullSlotAndCancellation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	name, directory := provisionSharedMemory(t, root)
	consumer, err := adapter.OpenSharedMemoryQueue(name, directory, true)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if second, err := adapter.OpenSharedMemoryQueue(name, directory, true); err == nil {
		_ = second.Close()
		t.Fatal("two consumers claimed the same native queue")
	}
	producer, err := adapter.OpenSharedMemoryQueue(name, directory, false)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	want := bytes.Repeat([]byte{0x00, 0xff, 0x80, 0x41}, 250)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := producer.WriteMessage(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := producer.WriteMessage(ctx, make([]byte, 1001)); !errors.Is(err, adapter.ErrSharedMemoryInvalid) {
		t.Fatal("oversize packet accepted", err)
	}
	fullCtx, fullCancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	err = producer.WriteMessage(fullCtx, []byte("must not overwrite"))
	fullCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("full queue did not observe deadline", err)
	}
	got := make([]byte, 1000)
	n, err := consumer.ReadMessage(ctx, got)
	if err != nil || n != len(want) || !bytes.Equal(got[:n], want) {
		t.Fatal("full-slot retry changed the published record", n, err)
	}
	done := make(chan error, 1)
	go func() { _, err := consumer.ReadMessage(t.Context(), got); done <- err }()
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read survived closed mapping")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wake an empty-queue reader")
	}
	reopened, err := adapter.OpenSharedMemoryQueue(name, directory, true)
	if err != nil {
		t.Fatal("consumer lock was not released", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSharedMemoryRejectsUnverifiedInputBeforeNativeIntake(t *testing.T) {
	f := newSharedMemoryFixture(t)
	request := core.Request{Version: 1, RequestID: "shm-invalid-auth", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
	for _, profile := range []string{"invalid", "signed", "other-realm"} {
		t.Run(profile, func(t *testing.T) {
			frame := sharedMemoryFrame(t, f, request, false)
			switch profile {
			case "invalid":
				frame.Envelope = "invalid"
			case "signed":
				frame.Profile = "signed"
				frame.Envelope = f.credentials.token(t, request, false)
			case "other-realm":
				credentials := *f.credentials
				credentials.realm = "other"
				frame.Envelope = credentials.token(t, request, true)
			}
			writeSharedMemoryFragments(t, f, sharedMemoryFragments(t, frame))
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			n, err := f.mailbox.ReadMessage(ctx, make([]byte, 1000))
			cancel()
			if n != 0 || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("unverified sender received a response", n, err)
			}
			rpcNoJobs(t, f.config.StateDir)
		})
	}
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name); len(messages) != 0 {
		t.Fatal("unverified shared-memory input reached native AIRC", messages)
	}
	sharedMemoryCall(t, f, core.Request{Version: 1, RequestID: "shm-after-invalid", Operation: core.CapabilitiesGet})
}
