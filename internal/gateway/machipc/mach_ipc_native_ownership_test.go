//go:build darwin && arm64

package machipc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
	"unsafe"
)

func TestComplexMessageRightsAreDestroyed(t *testing.T) {
	name := fmt.Sprintf("com.airc.gateway.complex.%d.%d", os.Getpid(), time.Now().UnixNano())
	listener, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	target, err := allocateReceivePort()
	if err != nil {
		t.Fatal(err)
	}
	if err := tInsertSendRight(target); err != nil {
		testReleaseReceive(t, target)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		status, errno := call6(libcMachPortDeallocate, machTaskSelf(), uintptr(target), 0, 0, 0, 0)
		if err := checkKR("release test send right", status, errno); err != nil {
			t.Errorf("release test send right: %v", err)
		}
		testReleaseReceive(t, target)
	})
	if refs := testSendRefCount(t, target); refs != 1 {
		t.Fatalf("initial send refs=%d want1", refs)
	}
	reply := testSendComplexPort(t, name, target)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := listener.Receive(ctx); !errors.Is(err, ErrInvalid) {
		testReleaseReceive(t, reply)
		t.Fatalf("complex Receive error=%v want ErrInvalid", err)
	}
	testReleaseReceive(t, reply)
	if refs := testSendRefCount(t, target); refs != 1 {
		t.Fatalf("mach_msg_destroy left copied send right: refs=%d want1", refs)
	}
}

func tInsertSendRight(port uint32) error {
	status, errno := call6(libcMachPortInsertRight, machTaskSelf(), uintptr(port), uintptr(port), 20, 0, 0)
	return checkKR("mach_port_insert_right", status, errno)
}

func TestReplyCancellationAndDeadRightCleanup(t *testing.T) {
	name := fmt.Sprintf("com.airc.gateway.reply.%d.%d", os.Getpid(), time.Now().UnixNano())
	listener, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	reply := testSendRaw(t, name, RequestID, []byte("retry"), false)
	req, err := listener.Receive(ctx)
	if err != nil {
		testReleaseReceive(t, reply)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := req.Close(); err != nil {
			t.Errorf("close received request: %v", err)
		}
	})
	canceled, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	if err := req.Reply(canceled, []byte("retry-response")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Reply=%v", err)
	}
	if err := req.Reply(ctx, []byte("retry-response")); err != nil {
		t.Fatal(err)
	}
	var response nativeTestMessage
	status, errno := call9(libcMachMsg, uintptr(unsafe.Pointer(&response.Header)), clientMsgReceive|clientMsgReceiveTimeout, 0, unsafe.Sizeof(response), uintptr(reply), 1000, 0, 0, 0)
	if err := checkKR("receive retried reply", status, errno); err != nil {
		testReleaseReceive(t, reply)
		t.Fatal(err)
	}
	testReleaseReceive(t, reply)

	if _, _, err := nativeTestChild(t, name, []byte("dead"), true); err != nil {
		t.Fatal(err)
	}
	deadReq, err := listener.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := deadReq.Close(); err != nil {
			t.Errorf("close dead reply request: %v", err)
		}
	})
	if err := deadReq.Reply(ctx, []byte("dead-response")); err == nil {
		t.Fatal("reply to exited client's receive right succeeded")
	}
	if err := deadReq.Close(); err != nil {
		t.Fatalf("close after failed dead-right reply: %v", err)
	}
}
