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
	"unsafe"
)

func TestMalformedMessagesAreDiscardedAndListenerContinues(t *testing.T) {
	name := fmt.Sprintf("com.airc.gateway.malformed.%d.%d", os.Getpid(), time.Now().UnixNano())
	listener, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for _, tc := range []struct {
		name   string
		id     int32
		body   []byte
		badPad bool
	}{
		{"wrong-id", RequestID + 1, []byte("bad"), false},
		{"nonzero-padding", RequestID, []byte("x"), true},
		{"oversized-header-limit", RequestID, bytes.Repeat([]byte("o"), MaxRequestBytes+1), false},
		{"oversized-receive-buffer", RequestID, bytes.Repeat([]byte("o"), MaxRequestBytes+1024), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := testSendRaw(t, name, tc.id, tc.body, tc.badPad)
			if _, err := listener.Receive(ctx); !errors.Is(err, ErrInvalid) {
				testReleaseReceive(t, reply)
				t.Fatalf("Receive error=%v want ErrInvalid", err)
			}
			testReleaseReceive(t, reply)
		})
	}
	reply := testSendRaw(t, name, RequestID, []byte("next-valid"), false)
	req, err := listener.Receive(ctx)
	if err != nil {
		testReleaseReceive(t, reply)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := req.Close(); err != nil {
			t.Errorf("close accepted request: %v", err)
		}
	})
	if string(req.Body) != "next-valid" {
		t.Fatalf("continued request=%q", req.Body)
	}
	if err := req.Reply(ctx, []byte("continued")); err != nil {
		t.Fatal(err)
	}
	var response nativeTestMessage
	status, errno := call9(libcMachMsg, uintptr(unsafe.Pointer(&response.Header)), clientMsgReceive|clientMsgReceiveTimeout, 0, unsafe.Sizeof(response), uintptr(reply), 1000, 0, 0, 0)
	if err := checkKR("receive continued reply", status, errno); err != nil {
		testReleaseReceive(t, reply)
		t.Fatal(err)
	}
	testReleaseReceive(t, reply)
}
