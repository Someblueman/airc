//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUDPExampleEncryptedPostReceiptAndNativeFact(t *testing.T) {
	f := newUDPFixture(t)
	mailbox := f.mailbox.LocalAddr().String()
	if err := f.mailbox.Close(); err != nil {
		t.Fatal(err)
	}
	publicPath, privatePath := writeUDPExampleKeys(t, f)
	common := []string{"--address", f.address, "--bind", mailbox, "--signing-public", publicPath, "--recipient-private", privatePath, "--realm", f.config.Realm, "--principal", f.config.Principals[0].ID, "--binding", f.config.Principals[0].Binding, "--route", "udp-return", "--signing-id", "udp-response", "--recipient-id", "udp-reader"}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	request := core.Request{Version: 1, RequestID: "udp-example-send", Operation: core.Send, Target: room, Message: "UDP example native post 🙂"}
	output := runUDPExample(t, common, f.credentials.token(t, request, true), "")
	firstCorrelation := output.CorrelationID
	if !strings.HasPrefix(firstCorrelation, "udp-") || len(firstCorrelation) != 36 {
		t.Fatalf("UDP example default correlation=%q", firstCorrelation)
	}
	if output.Result == nil || output.Result.Result == nil || output.Result.Result.RequestID != request.RequestID || !output.Result.Result.Durable {
		t.Fatalf("UDP example admission=%+v", output)
	}
	var accepted core.Result
	for attempt := 0; attempt < 8; attempt++ {
		receipt := core.Request{Version: 1, RequestID: "udp-example-receipt-" + string(rune('a'+attempt)), Operation: core.ReceiptGet, LookupID: request.RequestID}
		output = runUDPExample(t, common, f.credentials.token(t, receipt, true), "")
		if !strings.HasPrefix(output.CorrelationID, "udp-") || len(output.CorrelationID) != 36 || output.CorrelationID == firstCorrelation {
			t.Fatalf("UDP example reused its default correlation=%q", output.CorrelationID)
		}
		if output.Result == nil || output.Result.Result == nil || output.Result.Result.RequestID != request.RequestID {
			t.Fatalf("UDP example receipt=%+v", output)
		}
		accepted = *output.Result.Result
		if accepted.State == core.Accepted {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if accepted.State != core.Accepted || accepted.MessageID == "" {
		t.Fatalf("UDP example did not reach accepted=%+v", accepted)
	}
	history := core.Request{Version: 1, RequestID: "udp-example-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 8}
	historyOutput := runUDPExample(t, common, f.credentials.token(t, history, true), history.RequestID)
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if historyOutput.Result == nil || historyOutput.Result.Page == nil || len(native) != 1 || native[0].ID != accepted.MessageID || native[0].From != "fixture-author" || native[0].Target != room.Name || native[0].Message != request.Message || !reflect.DeepEqual(historyOutput.Result.Page.Messages, native) {
		t.Fatalf("UDP example history/native whole record mismatch: history=%+v native=%+v", historyOutput, native)
	}
}

func writeUDPExampleKeys(t *testing.T, f udpFixture) (string, string) {
	t.Helper()
	signing, err := auth.ReadResponseSigningKey(f.config.ResponseSigningKey.PrivateKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	signingDER, err := x509.MarshalPKIXPublicKey(signing.Public())
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(t.TempDir(), "response-public.pem")
	if err := os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: signingDER}), 0644); err != nil {
		t.Fatal(err)
	}
	recipientDER, err := x509.MarshalPKCS8PrivateKey(f.responseRecipient)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(t.TempDir(), "recipient-private.pem")
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: recipientDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return publicPath, privatePath
}

func runUDPExample(t *testing.T, args []string, token, correlation string) adapter.FrameResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmdArgs := []string{"run", "./examples/gateway/udp-client"}
	if correlation != "" {
		cmdArgs = append(cmdArgs, "--correlation", correlation)
	}
	cmdArgs = append(cmdArgs, args...)
	command := exec.CommandContext(ctx, "go", cmdArgs...)
	command.Dir = "../.."
	command.Stdin = bytes.NewReader([]byte(token + "\n"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("UDP example failed: %v output=%s", err, output)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(bytes.TrimSpace(output), &response); err != nil {
		t.Fatalf("UDP example output=%q: %v", output, err)
	}
	return response
}
