//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func buildXPCInvalidRequestClient(t *testing.T, upstreamName string) string {
	t.Helper()
	app, _ := buildXPCBundle(t, upstreamName)
	output := filepath.Join(app, "Contents", "MacOS", "client")
	source := filepath.Join("testdata", "xpc_invalid_request.c")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "xcrun", "--sdk", "macosx", "clang", "-std=c11", "-fblocks", "-Wall", "-Wextra", "-Werror", "-pedantic", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-fno-omit-frame-pointer", "-target", "arm64-apple-macos13.0", source, "-o", output)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build direct XPC SDK boundary client: %v: %s", err, output)
	}
	command = exec.CommandContext(ctx, "codesign", "--force", "--sign", "-", "--deep", app)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("sign XPC app with SDK boundary client: %v: %s", err, output)
	}
	return output
}

func runXPCInvalidRequest(ctx context.Context, client, mode string) ([]byte, error) {
	command := exec.CommandContext(ctx, client, mode)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("direct XPC SDK request: %w: %s", err, stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func xpcFrameForEnvelope(envelope string) adapter.FrameRequest {
	digest := sha256.Sum256([]byte(envelope))
	return adapter.FrameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: "encrypted", Envelope: envelope}
}

func TestXPCBridgeRejectsMalformedAndOversizedRequestsBeforeIntake(t *testing.T) {
	f := newXPCFixture(t, xpcCallerUID(), "device")
	base := core.Request{Version: 1, RequestID: "xpc-refusal", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not enter journal"}
	cases := []struct {
		name  string
		frame func() adapter.FrameRequest
	}{
		{name: "invalid-authentication", frame: func() adapter.FrameRequest { return xpcFrameForEnvelope("invalid compact token") }},
		{name: "signed-profile-refused", frame: func() adapter.FrameRequest {
			frame := machFrame(t, f.machIPCFixture, base)
			frame.Profile = "signed"
			return frame
		}},
		{name: "correlation-mismatch", frame: func() adapter.FrameRequest {
			frame := machFrame(t, f.machIPCFixture, base)
			frame.CorrelationID = strings.Repeat("0", 64)
			return frame
		}},
		{name: "stream-refused", frame: func() adapter.FrameRequest {
			frame := machFrame(t, f.machIPCFixture, base)
			frame.Stream = true
			return frame
		}},
		{name: "expired-envelope", frame: func() adapter.FrameRequest {
			now := time.Now().Unix()
			return xpcFrameForEnvelope(f.credentials.validityToken(t, base, now-600, now-300, true))
		}},
		{name: "wrong-audience", frame: func() adapter.FrameRequest {
			now := time.Now().Unix()
			realm := f.credentials.realm
			f.credentials.realm = "different-realm"
			token := f.credentials.validityToken(t, base, now, now+120, true)
			f.credentials.realm = realm
			return xpcFrameForEnvelope(token)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xpcNoReply(t, f, tc.frame())
			machAssertNoIntake(t, f.machIPCFixture)
		})
	}
	oversized := append(machFrameBytes(t, machFrame(t, f.machIPCFixture, base)), bytes.Repeat([]byte(" "), (16<<10)+1)...)
	oversized = oversized[:(16<<10)+1]
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if output, err := runXPCClient(ctx, f.client, oversized, false); err == nil || len(output) != 0 {
		t.Fatalf("XPC CLI accepted oversized input: bytes=%d err=%v", len(output), err)
	}
	malformedClient := buildXPCInvalidRequestClient(t, f.serviceName)
	for _, mode := range []string{"missing-frame", "wrong-frame-type", "extra-key", "oversized-frame"} {
		t.Run("sdk-"+mode, func(t *testing.T) {
			probeCtx, probeCancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer probeCancel()
			output, err := runXPCInvalidRequest(probeCtx, malformedClient, mode)
			if err != nil || string(output) != "rejected\n" {
				t.Fatalf("direct XPC SDK %s refusal output=%q err=%v", mode, output, err)
			}
			machAssertNoIntake(t, f.machIPCFixture)
		})
	}
	valid := core.Request{Version: 1, RequestID: "xpc-after-refusal", Operation: core.Send, Target: base.Target, Message: "bridge remains usable after malformed calls"}
	response, _ := xpcCall(t, f, valid)
	if response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatalf("valid request after refusal was not admitted: %+v", response)
	}
	receipt := machAwaitAccepted(t, f.machIPCFixture, valid.RequestID)
	if receipt.SendAttempts != 1 {
		t.Fatalf("post-refusal receipt=%+v", receipt)
	}
}

func TestXPCBridgeRefusesCallerUIDAndPrincipalMismatchBeforeIntake(t *testing.T) {
	uid := xpcCallerUID()
	wrongUID := uid + 1
	if wrongUID == ^uint32(0) {
		wrongUID = uid - 1
	}
	t.Run("uid-mismatch", func(t *testing.T) {
		f := newXPCFixture(t, wrongUID, "device")
		frame := machFrame(t, f.machIPCFixture, core.Request{Version: 1, RequestID: "xpc-wrong-uid", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "no intake"})
		xpcNoReply(t, f, frame)
		machAssertNoIntake(t, f.machIPCFixture)
	})
	t.Run("principal-mismatch", func(t *testing.T) {
		f := newXPCFixture(t, uid, "alternate")
		frame := machFrame(t, f.machIPCFixture, core.Request{Version: 1, RequestID: "xpc-wrong-principal", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "no intake"})
		xpcNoReply(t, f, frame)
		machAssertNoIntake(t, f.machIPCFixture)
	})
}
