//go:build darwin && arm64

package machipc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNativeRoundTripAndDuplicateRegistration(t *testing.T) {
	name := fmt.Sprintf("com.airc.gateway.unit.%d.%d", os.Getpid(), time.Now().UnixNano())
	listener, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	if _, err = Listen(name); !errors.Is(err, ErrNameInUse) {
		_ = listener.Close()
		t.Fatalf("duplicate listen error=%v want ErrNameInUse", err)
	}
	input := bytes.Repeat([]byte("m"), MaxRequestBytes)
	childResult := make(chan struct {
		body []byte
		pid  int
		err  error
	}, 1)
	go func() {
		body, pid, e := nativeTestChild(t, name, input, false)
		childResult <- struct {
			body []byte
			pid  int
			err  error
		}{body, pid, e}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := listener.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := req.Close(); err != nil {
			t.Errorf("close received request: %v", err)
		}
	})
	if !bytes.Equal(req.Body, input) {
		t.Fatal("request body mismatch")
	}
	if req.UID != uint32(os.Geteuid()) {
		t.Fatalf("audit uid=%d want %d", req.UID, os.Geteuid())
	}
	if req.PID <= 0 {
		t.Fatalf("audit pid=%d", req.PID)
	}
	response := bytes.Repeat([]byte("r"), MaxResponseBytes)
	if err := req.Reply(ctx, response); err != nil {
		t.Fatal(err)
	}
	if err := req.Reply(ctx, []byte("again")); !errors.Is(err, ErrReplied) {
		t.Fatalf("second reply=%v", err)
	}
	got := <-childResult
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.pid != int(req.PID) {
		t.Fatalf("audit pid=%d child pid=%d", req.PID, got.pid)
	}
	if !bytes.Equal(got.body, response) {
		t.Fatalf("response length/body mismatch: %d", len(got.body))
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := listener.Receive(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("receive after close=%v", err)
	}
	reopened, err := Listen(name)
	if err != nil {
		t.Fatalf("bootstrap name remained registered after close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReceiveCancellation(t *testing.T) {
	name := fmt.Sprintf("com.airc.gateway.cancel.%d.%d", os.Getpid(), time.Now().UnixNano())
	listener, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
	defer cancel()
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() { close(started); _, err := listener.Receive(ctx); result <- err }()
	<-started
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked receive error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked receive did not observe context deadline")
	}
	closeCtx, closeCancel := context.WithCancel(t.Context())
	defer closeCancel()
	started = make(chan struct{})
	result = make(chan error, 1)
	go func() { close(started); _, err := listener.Receive(closeCtx); result <- err }()
	<-started
	time.Sleep(10 * time.Millisecond)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("receive after close returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener close did not join pending receive")
	}
}
