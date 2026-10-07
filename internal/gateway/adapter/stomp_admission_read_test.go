//go:build darwin || linux

package adapter

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestSTOMPAdmissionParentDeadlineClosesNativeAttempt(t *testing.T) {
	for _, op := range []core.Operation{core.ContextRead, core.HistoryRead, core.Search} {
		t.Run(string(op), func(t *testing.T) {
			f := newAMQPAdmissionFixture(t)
			listener, err := net.Listen("unix", f.c.Bindings[0].Address)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted := make(chan net.Conn, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				conn, err := listener.Accept()
				if err == nil {
					accepted <- conn
				}
			}()
			defer func() {
				_ = listener.Close()
				<-finished
				select {
				case conn := <-accepted:
					_ = conn.Close()
				default:
				}
			}()
			request := core.Request{Version: 1, RequestID: "stomp-read", Operation: op, Target: amqpRequest().Target, Limit: 1}
			if op == core.ContextRead {
				request.ParentID = strings.Repeat("a", 32)
			}
			if op == core.Search {
				request.Query = "exact"
			}
			frame := stompAdmissionFrame(t, f, request, 0)
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			started := time.Now()
			slots := make(chan struct{}, 1)
			admission := admitSTOMP(ctx, f.d, f.identity, stompAdmissionRoute(f), frame, slots)
			if admission.refusal != "" || admission.result != nil || admission.failure == nil || !admission.failure.Transient || admission.requestID != request.RequestID || time.Since(started) > time.Second || len(slots) != 0 {
				t.Fatal(admission, time.Since(started))
			}
			// The socket peer deliberately does not register the native read session.
			// Expiry must end that real attempted connection, rather than merely return
			// a synthetic response while leaving the reader alive.
			select {
			case conn := <-accepted:
				defer conn.Close()
				if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, conn); err != nil {
					t.Fatal("native attempt did not close", err)
				}
			case <-time.After(time.Second):
				t.Fatal("read did not reach the native boundary")
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, request.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("read admitted mutation", err)
			}
		})
	}
}
