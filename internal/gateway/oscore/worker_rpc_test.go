//go:build darwin || linux

package oscore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	jose "github.com/go-jose/go-jose/v4"
)

func TestNativeOSCOREWorkerProtectedBlockwiseRPCAndReplay(t *testing.T) {
	f := configuredBridgeFixture(t, true)
	options := nativeOwnerOptions(t, f, "127.0.0.1:0")
	if err := f.store.ProvisionOSCORE(t.Context(), f.key); err != nil {
		t.Fatal(err)
	}
	v, err := auth.New(f.configuration)
	if err != nil {
		t.Fatal(err)
	}
	options.Dispatcher, err = dispatch.New(f.store, v, nil, nil, []core.Operation{core.Send, core.ReceiptGet})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := Start(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	issued, expires := time.Now().Unix(), time.Now().Unix()+300
	message := "native worker\r\n" + strings.Repeat("x", 1500)
	envelope := auth.MessageEnvelope{Version: 1, Audience: "fixture", IssuedAt: issued, ExpiresAt: expires, Request: core.Request{Version: 1, RequestID: "native-worker-rpc", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: message, IssuedAt: &issued, ExpiresAt: &expires}}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: f.signing}, (&jose.SignerOptions{}).WithHeader("kid", "native-signer"))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: "worker-rpc", Profile: "signed", Envelope: token})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	peerPath, sequencePath, requestPath, responsePath := filepath.Join(root, "peer.conf"), filepath.Join(root, "peer.seq"), filepath.Join(root, "request.json"), filepath.Join(root, "response.json")
	configuration, err := f.material.NativeConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(peerPath, configuration, 0600)
	clear(configuration)
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{sequencePath: []byte("0\n"), requestPath: request} {
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	client := os.Getenv("AIRC_OSCORE_COAP_CLIENT")
	if client == "" {
		client = filepath.Join(filepath.Dir(options.Binary), "libcoap", "coap-client")
	}
	client, err = ownedExecutable(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, client, "-v", "0", "-B", "10", "-m", "post", "-t", "50", "-b", "256", "-L", "3", "-G", "2", "-f", requestPath, "-E", peerPath+","+sequencePath, "-o", responsePath, "coap://"+owner.Addr().String()+"/v1/coap/rpc")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native client failed: %v %s", err, output)
	}
	data, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for i := 0; i < 2; i++ {
		var response adapter.FrameResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("native response %d: %v %s", i, err, data)
		}
		if response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != envelope.Request.RequestID || response.CorrelationID != "worker-rpc" {
			t.Fatal("wrong protected native response", response)
		}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("unexpected native response", err)
	}
	verified, err := v.AuthenticateSigned(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.store.Lookup(t.Context(), verified.Identity, envelope.Request.RequestID)
	if err != nil || job.Principal != "device" || job.Binding != "backend" || job.Request.Message != strings.ReplaceAll(message, "\r\n", "\n") || job.UpstreamID == "" || job.Result.SendAttempts != 0 {
		t.Fatal("wrong native intake", job, err)
	}
	state, err := f.store.LoadOSCORE(t.Context(), f.key)
	if err != nil || !state.ReceiverInitialized || state.ReceiverSequence < 2 || state.SenderEnd < 8 {
		t.Fatal("native packet state not durable", state, err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}
