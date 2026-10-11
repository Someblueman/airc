//go:build darwin || linux

package gateway_test

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestStdioPipeRejectsPartialFramesWithoutAdmission(t *testing.T) {
	_, c, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	binaryPath := gatewayBinary(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for _, variant := range []string{"header", "body"} {
		t.Run(variant, func(t *testing.T) {
			partial := startStdio(t, binaryPath, c)
			partialRequest := core.Request{Version: 1, RequestID: "partial-" + variant, Operation: core.Send, Target: target, Message: "must never be admitted"}
			partialBody, err := json.Marshal(frameRequest{Version: 1, CorrelationID: "partial", Profile: "plain", Request: credentials.token(t, partialRequest, false)})
			if err != nil {
				t.Fatal(err)
			}
			var partialHeader [4]byte
			binary.BigEndian.PutUint32(partialHeader[:], uint32(len(partialBody)))
			payload := append(partialHeader[:], partialBody[:len(partialBody)-1]...)
			if variant == "header" {
				payload = partialHeader[:2]
			}
			if _, err := partial.stdin.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := partial.stdin.Close(); err != nil {
				t.Fatal(err)
			}
			if err := partial.stdout.(*os.File).SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var output [1]byte
			if n, err := partial.stdout.Read(output[:]); n != 0 || !errors.Is(err, io.EOF) {
				t.Fatalf("partial frame unexpectedly produced stdout: n=%d err=%v", n, err)
			}
			done := make(chan error, 1)
			go func() { done <- partial.wait() }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("partial frame exited successfully")
				}
			case <-time.After(8 * time.Second):
				partial.command.Process.Kill()
				t.Fatal("partial stdio frame did not terminate")
			}
			reopen := startStdio(t, binaryPath, c)
			missing := core.Request{Version: 1, RequestID: "stdio-missing-receipt", Operation: core.ReceiptGet, LookupID: "partial-" + variant}
			reopen.write(t, frameRequest{Version: 1, CorrelationID: "partial-reopen", Profile: "plain", Request: credentials.token(t, missing, true)})
			if err := reopen.stdin.Close(); err != nil {
				t.Fatal(err)
			}
			response := reopen.read(t)
			if response.Version != 1 || response.CorrelationID != "partial-reopen" || response.Event != "error" || response.Error == nil || response.Error.Code != core.InvalidRequest {
				t.Fatalf("partial frame created a durable receipt: %+v", response)
			}
			if err := reopen.wait(); err != nil {
				t.Fatalf("partial reopen did not join: %v stderr=%s", err, reopen.stderr.String())
			}

		})
	}
}
