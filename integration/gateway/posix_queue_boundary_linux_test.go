//go:build linux && (arm64 || amd64)

package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testmqueue"
)

func assertPOSIXSilent(t *testing.T, socket *adapter.POSIXQueue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	n, _, err := socket.ReadMessage(ctx, make([]byte, 1001))
	if n != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected queue output bytes=%d err=%v", n, err)
	}
}

func TestPOSIXQueueUnverifiedInputHasNoResponseOrNativeIntake(t *testing.T) {
	testmqueue.Require(t)
	f := newPOSIXFixture(t)
	for _, kind := range []string{"unknown-profile", "malformed", "override", "oversize-producer-api", "incomplete"} {
		t.Run(kind, func(t *testing.T) {
			request := core.Request{Version: 1, RequestID: "udp-invalid-" + kind, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must never post"}
			credentials := *f.credentials
			frame := frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: credentials.token(t, request, true)}
			if kind == "unknown-profile" {
				frame.Profile = "unknown"
				frame.Request = credentials.token(t, request, false)
			}
			if kind == "malformed" {
				frame.Request = "[]"
			}
			if kind == "oversize-producer-api" {
				if err := f.source.WriteMessage(t.Context(), make([]byte, 1001)); !errors.Is(err, adapter.ErrPOSIXQueueInvalid) {
					t.Fatal(err)
				}
			} else if kind == "override" {
				body, err := json.Marshal(frame)
				if err != nil {
					t.Fatal(err)
				}
				body = append(body[:len(body)-1], []byte(`,"destination":"127.0.0.1:9"}`)...)
				for _, fragment := range webTransportDatagrams(body, [16]byte{5}) {
					if err := f.source.WriteMessage(t.Context(), fragment); err != nil {
						t.Fatal(err)
					}
				}
			} else if kind == "incomplete" {
				frame.Request = f.credentials.token(t, core.Request{Version: 1, RequestID: request.RequestID, Operation: core.Send, Target: request.Target, Message: strings.Repeat("p", 3500)}, true)
				parts := posixFragments(t, frame)
				if len(parts) < 2 {
					t.Fatal("partial witness missing")
				}
				writePOSIXFragments(t, f, parts[:1])
			} else {
				writePOSIXFragments(t, f, posixFragments(t, frame))
			}
			assertPOSIXSilent(t, f.mailbox)
			rpcNoJobs(t, f.config.StateDir)
		})
	}
	if messages := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatalf("invalid POSIX queue input posted %+v", messages)
	}
	// A well-formed request afterward gets exactly its own reply.
	posixCall(t, f, core.Request{Version: 1, RequestID: "udp-after-invalid", Operation: core.CapabilitiesGet})
	rpcNoJobs(t, f.config.StateDir)
}
