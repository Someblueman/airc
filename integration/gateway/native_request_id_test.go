//go:build darwin || linux

package gateway_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// nativeSend posts over native IRC with a request ID, as pkg/irc clients and
// the airc CLI do, and returns the message ID from the receipt.
func nativeSend(t *testing.T, socket, nick, token, room, body, requestID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := irc.DialContext(ctx, irc.Config{Nick: nick, Network: "unix", Addr: socket, IdentityToken: token, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SendWithID(room, body, requestID); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				t.Fatal("native connection closed before the receipt")
			}
			if receipt, ok := event.(*irc.SendReceiptEvent); ok && receipt.RequestID == requestID {
				return receipt.ID
			}
			if raw, ok := event.(*irc.RawEvent); ok && len(raw.Command) == 3 && raw.Command[0] == '4' && raw.Command != "422" {
				t.Fatalf("native send refused: %s %s", raw.Command, raw.Trailing)
			}
		case <-ctx.Done():
			t.Fatal("no native receipt")
		}
	}
}

// One request ID is one AIRC message whichever path carries it: a native
// send that landed and is retried over a gateway carrier (or the reverse)
// resolves to the first message, and its receipt reports that message.
func TestNativeAndGatewayRetriesShareOneRequestID(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	g := startAuthorGateway(t, authorConfig(t, socket, gatewayToken, config.Listener{ID: "web", Protocol: "http"}))
	base := "http://" + g.addresses["web"]
	const room = "#ids"
	gatewaySend := func(id, body, nick, token string) core.Result {
		t.Helper()
		var admitted core.Result
		if status := plainHTTP(t, http.MethodPost, base+"/v1/messages", sendRequest(id, room, body, nick, token), nil, &admitted); status != http.StatusAccepted {
			t.Fatalf("gateway send %s: HTTP %d %+v", id, status, admitted)
		}
		return waitReceipt(t, base, id, authorHeaders(nick, token))
	}

	// Native first; the send "timed out" after landing and is retried over HTTP.
	native := nativeSend(t, socket, "alice", "", room, "native first", "shared-1")
	retried := gatewaySend("shared-1", "native first", "alice", "")
	if retried.State != core.Accepted || retried.MessageID != native || retried.Failure != nil {
		t.Fatalf("gateway retry of a native send: %+v, native message %s", retried, native)
	}
	var again core.Result
	if status := plainHTTP(t, http.MethodGet, base+"/v1/requests/shared-1", nil, authorHeaders("alice", ""), &again); status != http.StatusOK || again.State != core.Accepted || again.MessageID != native {
		t.Fatalf("receipt after native-then-gateway retry: HTTP %d %+v", status, again)
	}

	// Gateway first; the client then retries natively with the same ID.
	first := gatewaySend("shared-2", "gateway first", "alice", "")
	if first.State != core.Accepted {
		t.Fatalf("gateway send: %+v", first)
	}
	if resent := nativeSend(t, socket, "alice", "", room, "gateway first", "shared-2"); resent != first.MessageID {
		t.Fatalf("native retry of a gateway send posted %s, gateway posted %s", resent, first.MessageID)
	}

	// A registered author: SASL natively, the same account token over HTTP.
	token := registerAccount(t, base, "carol")
	registeredNative := nativeSend(t, socket, "carol", token, room, "registered", "shared-3")
	if retried := gatewaySend("shared-3", "registered", "carol", token); retried.State != core.Accepted || retried.MessageID != registeredNative {
		t.Fatalf("registered gateway retry: %+v, native %s", retried, registeredNative)
	}

	// Another author reusing a request ID is its own message, on both paths.
	other := gatewaySend("shared-1", "native first", "bob", "")
	if other.State != core.Accepted || other.MessageID == native {
		t.Fatalf("another author's request ID collided: %+v", other)
	}
	for body, want := range map[string]int{"native first": 2, "gateway first": 1, "registered": 1} {
		if got := historyCount(t, base, room, body, authorHeaders("dave", "")); got != want {
			t.Errorf("%q appears %d times in history, want %d", body, got, want)
		}
	}
}

// registerAccount creates an AIRC account and returns its token.
func registerAccount(t *testing.T, base, nick string) string {
	t.Helper()
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var reply map[string]any
	if status := plainHTTP(t, http.MethodPost, base+"/v1/request", plainRequest(map[string]any{"request_id": "register-" + nick, "operation": "account.register"}, nick, token), nil, &reply); status != http.StatusOK {
		t.Fatalf("register %s: HTTP %d %v", nick, status, reply)
	}
	return token
}
