//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMessageMutationHTTPAndNativeReplay(t *testing.T) {
	f := newReactionTransportFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, f.socket, *target)
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	var saved []struct {
		request core.Request
		result  core.Result
	}
	parent := ""
	for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		r := messageMutationRequest("message-http-"+strings.TrimPrefix(string(op), "message."), op, target, parent)
		if op == core.MessageRetract {
			issued, expires := time.Now().Unix(), time.Now().Unix()+120
			r.IssuedAt, r.ExpiresAt = &issued, &expires
			status, admission := contextHTTPCall(t, &h2, f.cred.token(t, r, false), false)
			if status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil {
				t.Fatalf("signed mutation: %d %+v", status, admission)
			}
		} else {
			roomHeaderPost(t, &h2, r, 2)
		}
		result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
		message := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
		assertMessageMutationFacts(t, result, r, message)
		compareMessageHistory(t, f, r, "http", message)
		saved = append(saved, struct {
			request core.Request
			result  core.Result
		}{r, result})
		parent = result.MessageID
	}
	for _, prior := range saved {
		roomHeaderPost(t, f, prior.request, 1)
		replay := waitHTTPPinResult(t, f, prior.request.RequestID, core.Accepted)
		if !reflect.DeepEqual(replay, prior.result) {
			t.Fatal("replay changed historical facts", replay, prior.result)
		}
	}
	assertNoMessageMutation(t, observer)
}

func TestMessageMutationHTTPFramedAndWebSocket(t *testing.T) {
	f := newReactionTransportFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, f.socket, *target)
	for _, carrier := range []struct {
		name string
		call func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
		{"framed", func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "message-frame")
		}},
		{"websocket", func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "message-ws")
		}},
	} {
		parent := ""
		for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
			r := messageMutationRequest("message-"+carrier.name+"-"+strings.TrimPrefix(string(op), "message."), op, target, parent)
			status, admission := carrier.call(f.cred.token(t, r, true))
			if status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil {
				t.Fatalf("%s admission: %d %+v", carrier.name, status, admission)
			}
			result := pollReactionReceipt(t, f, r, carrier.name)
			message := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
			assertMessageMutationFacts(t, result, r, message)
			compareMessageHistory(t, f, r, carrier.name, message)
			parent = result.MessageID
		}
	}
}
