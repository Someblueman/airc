//go:build darwin || linux

package gateway_test

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func exerciseMessageMutationStream(t *testing.T, observer *irc.Client, call func(core.Request, string) adapter.FrameResponse, prefix string) (core.Request, core.Result) {
	t.Helper()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := ""
	var first core.Request
	var saved core.Result
	for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		r := messageMutationRequest("post-"+prefix+"-"+strings.TrimPrefix(string(op), "message."), op, target, parent)
		issued, expires := time.Now().Unix(), time.Now().Unix()+120
		r.IssuedAt, r.ExpiresAt = &issued, &expires
		if response := call(r, r.RequestID); response.Error != nil || response.Result == nil {
			t.Fatalf("%s admission: %+v", prefix, response)
		}
		result := waitPollMutationResult(t, call, r)
		message := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
		assertMessageMutationFacts(t, result, r, message)
		if op == core.MessageAction {
			first, saved = r, result
		}
		parent = result.MessageID
	}
	if response := call(first, first.RequestID+"-replay"); response.Error != nil {
		t.Fatal("replayfailed", response)
	}
	replay := waitPollMutationResult(t, call, first)
	if !reflect.DeepEqual(saved, replay) {
		t.Fatal("streamreplay changed original evidence", saved, replay)
	}
	assertNoMessageMutation(t, observer)
	return first, saved
}

func TestMessageMutationUnixStdioAndRestart(t *testing.T) {
	conn, credentials, stop, _, socket := unixStreamFixture(t)
	defer conn.Close()
	target := core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, socket, target)
	call := func(r core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: credentials.token(t, r, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("Unix response: %+v", response)
		}
		return response
	}
	exerciseMessageMutationStream(t, observer, call, "unix")
	stop()
	socket, config, stdioCredentials, stdioObserver := stdioFixtureConfig(t)
	defer stdioObserver.Close()
	observer = newReplySignalObserver(t, socket, target)
	process := startStdio(t, gatewayBinary(t), config)
	call = func(r core.Request, correlation string) adapter.FrameResponse {
		process.write(t, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: stdioCredentials.token(t, r, true)})
		response := process.read(t)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("stdio response: %+v", response)
		}
		return response
	}
	first, saved := exerciseMessageMutationStream(t, observer, call, "stdio")
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatal(err)
	}
	process = startStdio(t, gatewayBinary(t), config)
	if response := call(first, "post-restart-replay"); response.Error != nil {
		t.Fatal(response)
	}
	replay := waitPollMutationResult(t, call, first)
	if !reflect.DeepEqual(saved, replay) {
		t.Fatal("restart changed historical action receipt", saved, replay)
	}
	assertNoMessageMutation(t, observer)
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatal(err)
	}
}

func TestMessageMutationFramedTLS(t *testing.T) {
	conn, credentials, stop, socket := framedTLSCardFixture(t)
	defer conn.Close()
	defer stop()
	observer := newReplySignalObserver(t, socket, core.Target{Kind: "room", Name: "#gateway-fixture"})
	call := func(r core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: credentials.token(t, r, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("TLS response: %+v", response)
		}
		return response
	}
	exerciseMessageMutationStream(t, observer, call, "tls")
}
