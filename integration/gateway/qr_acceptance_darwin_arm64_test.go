//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/service"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestQRNativeImportAndDurableReplayRejection(t *testing.T) {
	tool := qrTool(t)
	f := newOfflineBundleFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	bodies := []string{"QR sequence\nwhole Unicode 世界 🙂 $(touch must-not-execute)", "QR large body " + strings.Repeat("x", 3500)}
	tokens := make([]string, len(bodies))
	expires := time.Now().Unix() + 240
	for i, body := range bodies {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("qr-native-%d", i), Operation: core.Send, Target: room, Message: body}
		tokens[i] = f.credentials.validityToken(t, request, expires-300, expires, false)
	}
	input := qrSignedBundle(t, tokens)
	images := qrEncode(t, tool, input, filepath.Join(t.TempDir(), "images"))
	if len(images) < 3 {
		t.Fatal("native qualification did not exercise a sequence", len(images))
	}
	slices.Reverse(images)
	decoded := filepath.Join(t.TempDir(), "decoded.json")
	if err := qrDecode(t, tool, decoded, images); err != nil {
		t.Fatal("decode reordered native QR sequence", err)
	}
	qrAssertSameFileBytes(t, input, decoded)
	report := qrImportFile(t, f, decoded, true)
	if len(report.Entries) != len(bodies) {
		t.Fatal("missing native QR import entries", report)
	}
	for i, entry := range report.Entries {
		if !entry.Imported || entry.ReplayRejected || entry.Author != fixtureAuthor || !entry.Result.Durable || entry.Result.State != core.Accepted || entry.Result.SendAttempts != 1 || entry.Result.ExpiresAt != expires {
			t.Fatal("QR import did not preserve signed identity, validity or acceptance", entry)
		}
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == entry.Result.MessageID })
		if event.From != "fixture-author" || event.Target != room.Name || event.Message != bodies[i] {
			t.Fatal("independent native observer mismatch", event)
		}
	}
	// Decode the retained images in a fresh process, then retry through the real
	// importer. Neither QR reassembly nor a new filename creates fresh authority.
	retryFile := filepath.Join(t.TempDir(), "retry.json")
	if err := qrDecode(t, tool, retryFile, images); err != nil {
		t.Fatal("decode retained QR images", err)
	}
	replay := qrImportFile(t, f, retryFile, false)
	if len(replay.Entries) != len(bodies) {
		t.Fatal("missing QR replay report", replay)
	}
	for i, entry := range replay.Entries {
		if entry.Imported || !entry.ReplayRejected || entry.Result.State != core.Accepted || entry.Result.MessageID != report.Entries[i].Result.MessageID || entry.Result.SendAttempts != 1 || entry.Result.ExpiresAt != expires {
			t.Fatal("QR replay admitted new work or changed the original result", entry)
		}
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != len(bodies) {
		t.Fatal("QR retry lost or duplicated native posts", native)
	}
	s, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal("QR importer retained journal ownership", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestQRNativeImportChecksBeforeIntake(t *testing.T) {
	tool := qrTool(t)
	f := newOfflineBundleFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	request := core.Request{Version: 1, RequestID: "qr-refusal", Operation: core.Send, Target: room, Message: "must not deliver from invalid QR authority"}
	for _, kind := range []string{"read", "body"} {
		t.Run(kind, func(t *testing.T) {
			refused := request
			if kind == "read" {
				refused = core.Request{Version: 1, RequestID: request.RequestID, Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
			} else {
				refused.Message = strings.Repeat("x", 4097)
			}
			token := f.credentials.token(t, refused, false)
			input := qrSignedBundle(t, []string{token})
			images := qrEncode(t, tool, input, filepath.Join(t.TempDir(), "images"))
			decoded := filepath.Join(t.TempDir(), "decoded.json")
			if err := qrDecode(t, tool, decoded, images); err != nil {
				t.Fatal("QR framing should preserve the refused request bytes", err)
			}
			qrAssertSameFileBytes(t, input, decoded)
			if report := qrImportFile(t, f, decoded, false); report.Version != 0 {
				t.Fatal("refused request returned an intake report", report)
			}
			if _, err := os.Stat(filepath.Join(f.config.StateDir, "journal.sqlite")); !os.IsNotExist(err) {
				t.Fatal("refused QR request opened the journal", err)
			}
		})
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 0 {
		t.Fatal("refused QR request delivered native work", native)
	}
	request.Message = "fresh QR control after refused requests 世界"
	input := qrSignedBundle(t, []string{f.credentials.token(t, request, false)})
	images := qrEncode(t, tool, input, filepath.Join(t.TempDir(), "control-images"))
	decoded := filepath.Join(t.TempDir(), "control.json")
	if err := qrDecode(t, tool, decoded, images); err != nil {
		t.Fatal("decode matched valid QR control", err)
	}
	control := qrImportFile(t, f, decoded, true)
	if len(control.Entries) != 1 || !control.Entries[0].Imported || control.Entries[0].Result.State != core.Accepted || control.Entries[0].Result.SendAttempts != 1 {
		t.Fatal("matched valid QR control failed", control)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 1 || native[0].From != "fixture-author" || native[0].Target != room.Name || native[0].Message != request.Message {
		t.Fatal("wrong matched valid QR native control", native)
	}
}

func qrSignedBundle(t *testing.T, tokens []string) string {
	t.Helper()
	data, err := json.Marshal(adapter.OfflineBundle{Version: 1, Requests: rawRequests(tokens)})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "signed-bundle.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	return input
}

func qrAssertSameFileBytes(t *testing.T, first, second string) {
	t.Helper()
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("QR codec changed signed bundle bytes", err)
	}
}

func qrImportFile(t *testing.T, f *fileMailboxFixture, input string, success bool) service.OfflineImportReport {
	t.Helper()
	before, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, f.binary, "import-bundle", "--file", f.path, "--input", input)
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	output, err := command.Output()
	if ctx.Err() != nil || (err == nil) != success {
		t.Fatalf("QR bundle import success=%v: err=%v context=%v stderr=%s", success, err, ctx.Err(), diagnostics.Bytes())
	}
	if !success {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("QR refusal crashed or returned unexpected exit: %v stderr=%s", err, diagnostics.Bytes())
		}
	} else if diagnostics.Len() != 0 {
		t.Fatalf("successful QR importer emitted diagnostics: %s", diagnostics.Bytes())
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("QR importer changed its decoded source", err)
	}
	var report service.OfflineImportReport
	if len(output) != 0 && (json.Unmarshal(output, &report) != nil || report.Version != 1) {
		t.Fatal("invalid QR import report")
	}
	return report
}
