//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUnixStreamFramingAndEncryptedProfileBoundary(t *testing.T) {
	conn, credentials, stop, address, _ := unixStreamFixture(t)
	defer conn.Close()
	discovery := core.Request{Version: 1, RequestID: "unix-discovery", Operation: core.CapabilitiesGet}
	writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "signed", Profile: "signed", Envelope: credentials.token(t, discovery, false)})
	if response := readFrame(t, conn); response.Error == nil || response.Error.Code != core.InvalidRequest {
		t.Fatalf("unencrypted signed profile was accepted: %+v", response)
	}
	writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "unknown-author", Profile: "encrypted", Envelope: "not-an-envelope"})
	if response := readFrame(t, conn); response.Error == nil || response.Error.Code != core.AuthenticationFailed || response.Result != nil {
		t.Fatalf("unauthorized same-UID process obtained a result: %+v", response)
	}
	writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "encrypted", Profile: "encrypted", Envelope: credentials.token(t, discovery, true)})
	if response := readFrame(t, conn); response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("encrypted Unix request failed: %+v", response)
	}
	cliRequest := core.Request{Version: 1, RequestID: "unix-cli", Operation: core.CapabilitiesGet}
	cliCtx, cancelCLI := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancelCLI()
	command := exec.CommandContext(cliCtx, "go", "run", "./examples/gateway/framed-client", "--network", "unix", "--address", address, "--profile", "encrypted", "--correlation", "unix-cli")
	command.Dir = "../.."
	command.Stdin = strings.NewReader(credentials.token(t, cliRequest, true))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Unix framed-client failed: %v %s", err, output)
	}
	var cliResponse adapter.FrameResponse
	if err := json.Unmarshal(output, &cliResponse); err != nil || cliResponse.CorrelationID != "unix-cli" || cliResponse.Result == nil || cliResponse.Result.Capabilities == nil {
		t.Fatalf("Unix framed-client returned invalid response: %v %s", err, output)
	}
	var headerBytes [4]byte
	var oneByte [1]byte
	malformed, err := net.DialTimeout("unix", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	bad := []byte(`{"version":1,"version":1}`)
	binary.BigEndian.PutUint32(headerBytes[:], uint32(len(bad)))
	if _, err := malformed.Write(append(headerBytes[:], bad...)); err != nil {
		t.Fatal(err)
	}
	_ = malformed.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := malformed.Read(oneByte[:]); !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("malformed Unix frame did not close: %v", err)
	}
	_ = malformed.Close()
	partial, err := net.DialTimeout("unix", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint32(headerBytes[:], 64)
	if _, err := partial.Write(append(headerBytes[:], '{')); err != nil {
		t.Fatal(err)
	}
	_ = partial.SetReadDeadline(time.Now().Add(7 * time.Second))
	if _, err := partial.Read(oneByte[:]); err == nil || !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("partial Unix frame did not close cleanly: %v", err)
	}
	_ = partial.Close()
	stop()
}
