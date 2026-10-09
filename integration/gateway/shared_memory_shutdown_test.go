//go:build (darwin || linux) && arm64

package gateway_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSharedMemoryShutdownJoinsBlockedWholePageWrite(t *testing.T) {
	runNativeQueueShutdownJoinsBlockedWholePageWrite(t, newSharedMemoryFixture)
}

func TestMappedFileShutdownJoinsBlockedWholePageWrite(t *testing.T) {
	runNativeQueueShutdownJoinsBlockedWholePageWrite(t, newMappedFileFixture)
}

func runNativeQueueShutdownJoinsBlockedWholePageWrite(t *testing.T, newFixture func(*testing.T) nativeQueueFixture) {
	t.Helper()
	f := newFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for i := range 2 {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("%s-page-seed-%d", f.kind, i), Operation: core.Send, Target: target, Message: strings.Repeat("b", 2800)}
		if receipt := sharedMemoryAccepted(t, f, request); receipt.State != core.Accepted {
			t.Fatal("history seed not accepted", receipt)
		}
	}
	request := core.Request{Version: 1, RequestID: f.kind + "-blocked-history", Operation: core.HistoryRead, Target: target, Limit: 2, Start: "retained"}
	writeSharedMemoryFragments(t, f, sharedMemoryFragments(t, sharedMemoryFrame(t, f, request, false)))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := make([]byte, 1000)
	n, err := f.mailbox.ReadMessage(ctx, first)
	if err != nil || n != len(first) || string(first[:4]) != "AGD1" || binary.BigEndian.Uint16(first[24:26]) != 0 || binary.BigEndian.Uint16(first[26:28]) < 3 {
		t.Fatal("large whole page did not start a multi-fragment response", n, err)
	}
	// The next READY slot is unread and cannot be overwritten. The response
	// contains at least a third fragment, so publication cannot complete yet.
	mapping, err := openNativeQueueView(f.kind, f.responseAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer mapping.Close()
	waitSharedMemorySlotState(t, mapping, 2, 5*time.Second)
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal("blocked shared-memory response shutdown", f.process.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shared-memory response writer did not join shutdown")
	}
	consumer, err := openNativeQueue(f.kind, f.address, f.ingressDirectory, true)
	if err != nil {
		t.Fatal("gateway did not release the consumer directory", err)
	}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
}
