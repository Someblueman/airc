//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/service"
)

// This producer encodes its own wire bytes and identity, independently of the decoder.
type bpProducer struct {
	source, destination          string
	creation, sequence, lifetime uint64
	payload                      string
}

func bpUint(b []byte, major byte, n uint64) []byte {
	switch {
	case n < 24:
		return append(b, major<<5|byte(n))
	case n <= 255:
		return append(b, major<<5|24, byte(n))
	case n <= 65535:
		return binary.BigEndian.AppendUint16(append(b, major<<5|25), uint16(n))
	case n <= 4294967295:
		return binary.BigEndian.AppendUint32(append(b, major<<5|26), uint32(n))
	default:
		return binary.BigEndian.AppendUint64(append(b, major<<5|27), n)
	}
}
func bpString(b []byte, major byte, s string) []byte {
	return append(bpUint(b, major, uint64(len(s))), s...)
}
func (p bpProducer) id() string {
	b := append([]byte("airc-bpv7-identity-v1\x00"), p.source...)
	b = append(b, 0)
	b = binary.BigEndian.AppendUint64(b, p.creation)
	b = binary.BigEndian.AppendUint64(b, p.sequence)
	sum := sha256.Sum256(b)
	return "bp7-" + base64.RawURLEncoding.EncodeToString(sum[:])
}
func (p bpProducer) wire() []byte {
	b := []byte{0x89, 7, 4, 2, 0x82, 1}
	b = bpString(b, 3, p.destination[4:])
	b = append(b, 0x82, 1)
	b = bpString(b, 3, p.source[4:])
	b = append(b, 0x82, 1, 0, 0x82)
	b = bpUint(b, 0, p.creation)
	b = bpUint(b, 0, p.sequence)
	b = bpUint(b, 0, p.lifetime)
	b = append(b, 0x44, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.Checksum(b, crc32.MakeTable(crc32.Castagnoli)))
	out := append([]byte{0x9f}, b...)
	out = append(out, 0x85, 1, 1, 0, 0)
	out = bpString(out, 2, p.payload)
	return append(out, 0xff)
}
func newBPFixture(t *testing.T) *fileMailboxFixture {
	t.Helper()
	f := newFileMailboxFixture(t)
	f.config.Listeners = []config.Listener{{ID: "bp", Protocol: "bpv7", Enabled: true, Operations: []core.Operation{core.Send}, BundleProtocol: &config.BundleProtocol{Source: "dtn://producer/", Destination: "dtn://gateway/send", Principal: f.config.Principals[0].ID, Target: core.Target{Kind: "room", Name: "#gateway-fixture"}}}}
	bpSaveConfig(t, f)
	return f
}
func bpSaveConfig(t *testing.T, f *fileMailboxFixture) {
	t.Helper()
	b, err := json.Marshal(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func bpIntent(t *testing.T, f *fileMailboxFixture, seq uint64, issued, expires int64, body string) bpProducer {
	t.Helper()
	p := bpProducer{source: "dtn://producer/", destination: "dtn://gateway/send", creation: uint64(issued-946684800) * 1000, sequence: seq, lifetime: uint64(expires-issued) * 1000}
	r := core.Request{Version: 1, RequestID: p.id(), Operation: core.Send, Target: &f.config.Listeners[0].BundleProtocol.Target, Message: body}
	p.payload = f.credentials.validityToken(t, r, issued, expires, false)
	return p
}
func bpCall(t *testing.T, f *fileMailboxFixture, data []byte, success bool) service.OfflineImportReport {
	t.Helper()
	input := filepath.Join(f.directory, "input.bpv7")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.binary, "import-bpv7", "--file", f.path, "--input", input)
	var diag bytes.Buffer
	cmd.Stderr = &diag
	out, err := cmd.Output()
	if (err == nil) != success || ctx.Err() != nil {
		t.Fatalf("BPv7 success=%v error=%v diagnostics=%s", success, err, diag.Bytes())
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("input changed", err)
	}
	var report service.OfflineImportReport
	if len(out) > 0 && (json.Unmarshal(out, &report) != nil || report.Version != 1) {
		t.Fatal("invalid report")
	}
	return report
}
