//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestAnonymousUDPClientSendReadReplayAndBounds(t *testing.T) {
	f := newPacketFixtureConfigured(t, "udp", func(c *config.Config, _, _ *net.UDPConn) {
		c.Listeners[0].AnonymousPrincipal = c.Principals[0].ID
		c.Listeners[0].Operations = []core.Operation{core.Send, core.ReceiptGet, core.HistoryRead}
		c.Principals[0].Operations = c.Listeners[0].Operations
		c.Principals[0].BearerTokenFile, c.Principals[0].SigningKeys = "", nil
		c.RecipientKeys, c.ResponseSigningKey = nil, nil
		c.ReturnRoutes = []config.ReturnRoute{}
	}, listenUDPFixtureSocket)
	assertAnonymousUDPRejectsMalformed(t, f.address)
	root, binary, err := buildExampleClient("udp-anonymous-client")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	call := func(r core.Request, budget string, failure core.ErrorCode) adapter.AnonymousUDPResponse {
		t.Helper()
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "--address", f.address, "--max-response-bytes", budget)
		cmd.Env = []string{"HOME=" + t.TempDir()}
		cmd.Stdin = bytes.NewReader(data)
		var output, diagnostics bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &diagnostics
		err = cmd.Run()
		var response adapter.AnonymousUDPResponse
		if json.Unmarshal(output.Bytes(), &response) != nil || response.RequestID != r.RequestID {
			t.Fatalf("client response: %v %s %s", err, output.String(), diagnostics.String())
		}
		if failure == "" && (err != nil || response.Error != nil) || failure != "" && (err == nil || response.Error == nil || response.Error.Code != failure) {
			t.Fatalf("client result: %v %+v %s", err, response, diagnostics.String())
		}
		maxBytes, err := strconv.Atoi(budget)
		if err != nil || len(bytes.TrimSpace(output.Bytes())) > maxBytes {
			t.Fatal("response exceeded its budget")
		}
		return response
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	send := core.Request{Version: 1, RequestID: "anonymous-send", Operation: core.Send, Target: room, Message: strings.Repeat("anonymous 🙂 ", 40)}
	if first := call(send, "4096", ""); first.Result == nil || first.Result.Result == nil || !first.Result.Result.Durable {
		t.Fatalf("missing admission: %+v", first)
	}
	var accepted core.Result
	for range 8 {
		response := call(core.Request{Version: 1, RequestID: "receipt", Operation: core.ReceiptGet, LookupID: send.RequestID}, "4096", "")
		accepted = *response.Result.Result
		if accepted.State == core.Accepted {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if accepted.State != core.Accepted || accepted.Accepted == nil || !*accepted.Accepted || accepted.Persisted == nil || !*accepted.Persisted || accepted.SendAttempts != 1 {
		t.Fatalf("native acceptance missing: %+v", accepted)
	}
	history := core.Request{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 10}
	call(history, "512", core.CapacityExceeded)
	page := call(history, "4096", "").Result.Page
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if page == nil || len(page.Messages) != 1 || len(native) != 1 || page.Messages[0].ID != accepted.MessageID || native[0].ID != accepted.MessageID || native[0].From != "fixture-author" || native[0].Message != send.Message {
		t.Fatalf("history/native mismatch: %+v %+v", page, native)
	}
	denied := send
	denied.RequestID, denied.Target = "forbidden", &core.Target{Kind: "room", Name: "#other"}
	call(denied, "4096", core.Forbidden)
	conflict := send
	conflict.Message = "different intent under the same ID"
	call(conflict, "4096", core.IDConflict)
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
	case <-time.After(7 * time.Second):
		t.Fatal("anonymous gateway did not join shutdown")
	}
	p := startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, p)
	f.address = mcpGatewayAddress(t, p)
	replay := call(send, "4096", "").Result.Result
	if replay.MessageID != accepted.MessageID || replay.SendAttempts != 1 || len(call(history, "4096", "").Result.Page.Messages) != 1 {
		t.Fatal("replay changed the accepted message after restart")
	}
}

func assertAnonymousUDPRejectsMalformed(t *testing.T, address string) {
	t.Helper()
	conn, err := net.Dial("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for name, data := range map[string][]byte{
		"spoofed_identity": []byte(`{"version":1,"request_id":"spoof","operation":"receipt.get","lookup_id":"x","principal":"admin"}`),
		"duplicate_field":  []byte(`{"version":1,"request_id":"duplicate","operation":"receipt.get","operation":"send","lookup_id":"x"}`),
		"oversized":        bytes.Repeat([]byte(" "), core.MaxEnvelopeBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := conn.Write(data); err != nil {
				if len(data) > core.MaxEnvelopeBytes && errors.Is(err, syscall.EMSGSIZE) {
					t.Skip("host UDP limit prevents sending an oversized datagram")
				}
				t.Fatal(err)
			}
			if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Read(make([]byte, core.MaxEnvelopeBytes)); !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("malformed datagram was not silently discarded: %v", err)
			}
		})
	}
}

func TestAuthenticatedUDPDoesNotAcceptAnonymousJSON(t *testing.T) {
	f := newUDPFixture(t)
	conn, err := net.Dial("udp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data := []byte(`{"version":1,"request_id":"anonymous-denied","operation":"send","target":{"kind":"room","name":"#gateway-fixture"},"message":"must not post"}`)
	data = append(data, bytes.Repeat([]byte(" "), 4096-len(data))...)
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 4096)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("authenticated listener did not silently discard anonymous input: %v", err)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(native) != 0 {
		t.Fatal("anonymous input reached AIRC through an authenticated listener")
	}
}
