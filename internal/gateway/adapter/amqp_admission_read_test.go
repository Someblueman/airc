//go:build darwin || linux

package adapter

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestAMQPAdmissionBoundedReadDeadlineClosesNativeAttempt(t *testing.T) {
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
			request := core.Request{Version: 1, RequestID: "amqp-read", Operation: op, Target: amqpRequest().Target, Limit: 1}
			if op == core.ContextRead {
				request.ParentID = strings.Repeat("a", 32)
			}
			if op == core.Search {
				request.Query = "exact"
			}
			in := f.incoming(t, request, 0)
			headerValue := amqpwire.Value{
				Descriptor: &amqpwire.Value{Code: 0x80, Number: 0x70},
				Children:   []amqpwire.Value{{Code: 0xc0, Children: []amqpwire.Value{{Code: 0x40}, {Code: 0x40}, {Code: 0x70, Number: 150}}}},
			}
			header, err := amqpwire.EncodeValue(headerValue, 65536)
			if err != nil {
				t.Fatal(err)
			}
			in.encoded = append(header, in.encoded...)
			started := time.Now()
			slots := make(chan struct{}, 1)
			admission := admitAMQP(t.Context(), f.d, f.identity, f.route(), in, slots)
			if admission.condition != "" || admission.result != nil || admission.failure == nil || !admission.failure.Transient || admission.requestID != request.RequestID || time.Since(started) > time.Second || len(slots) != 0 {
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
