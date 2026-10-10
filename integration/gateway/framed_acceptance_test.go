//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestFramedIPv4AuthenticatedSendAndBoundedResume(t *testing.T) {
	conn, credentials, socket, stop := framedFixture(t, false)
	if remote, ok := conn.RemoteAddr().(*net.TCPAddr); !ok || remote.IP.To4() == nil {
		t.Fatal("fixture did not use IPv4", conn.RemoteAddr())
	}
	framedAuthenticatedSendAndBoundedResume(t, conn, credentials, socket, true)
	stop()
}

func TestFramedIPv6AuthenticatedSendAndBoundedResume(t *testing.T) {
	conn, credentials, socket, stop := framedFixtureAddress(t, false, "[::1]:0")
	if remote, ok := conn.RemoteAddr().(*net.TCPAddr); !ok || remote.IP.To4() != nil || remote.IP.To16() == nil {
		t.Fatal("fixture did not use IPv6", conn.RemoteAddr())
	}
	framedAuthenticatedSendAndBoundedResume(t, conn, credentials, socket, true)
	stop()
}

func TestFramedTLSAuthenticatedSendAndBoundedResume(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		name := "signed"
		if encrypted {
			name = "encrypted"
		}
		t.Run(name, func(t *testing.T) {
			conn, credentials, socket, stop := framedFixture(t, true)
			state := conn.(*tls.Conn).ConnectionState()
			if state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != adapter.FramedALPN {
				t.Fatal("TLS verification or protocol mismatch", state)
			}
			writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "bad-auth", Profile: name, Envelope: "invalid"})
			if response := readFrame(t, conn); response.CorrelationID != "bad-auth" || response.Error == nil || response.Error.Code != core.AuthenticationFailed || response.Result != nil {
				t.Fatal("TLS accepted invalid authentication", response)
			}
			framedAuthenticatedSendAndBoundedResume(t, conn, credentials, socket, encrypted)
			stop()
		})
	}
}

func framedAuthenticatedSendAndBoundedResume(t *testing.T, conn adapter.FramedStream, credentials *detachedCredentials, socket string, encrypted bool) {
	t.Helper()
	observer := dial(t, socket, "framed-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "framed-observer" })
	correlation := 0
	call := func(id string, request core.Request, envelope string) *dispatch.DetachedResult {
		t.Helper()
		correlation++
		id = fmt.Sprintf("%s-%d", id, correlation)
		if envelope == "" {
			envelope = credentials.token(t, request, encrypted)
		}
		profile := "signed"
		if encrypted {
			profile = "encrypted"
		}
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: id, Profile: profile, Envelope: envelope})
		response := readFrame(t, conn)
		if response.CorrelationID != id || response.Error != nil || response.Result == nil {
			t.Fatal("framed operation failed", response)
		}
		return response.Result
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	var observed []*irc.MessageEvent
	for _, id := range []string{"framed-one", "framed-two"} {
		request := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: room, Message: id + "\nwhole body 🙂"}
		envelope := credentials.token(t, request, encrypted)
		admitted := call(id, request, envelope)
		if admitted.Result == nil || !admitted.Result.Durable {
			t.Fatal("framed send was not durable", admitted)
		}
		var accepted *core.Result
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			result := call(id+"-receipt", core.Request{Version: 1, RequestID: id + "-receipt", Operation: core.ReceiptGet, LookupID: id}, "")
			if result.Result != nil && result.Result.State == core.Accepted {
				accepted = result.Result
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if accepted == nil {
			t.Fatal("framed send did not reach native acceptance")
		}
		seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
		if seen.From != "fixture-author" || seen.Target != room.Name || seen.Message != request.Message {
			t.Fatal("framed native observer mismatch", seen)
		}
		observed = append(observed, seen)
		replayed := call(id+"-repeat", request, envelope)
		if replayed.Result == nil || replayed.Result.MessageID != seen.ID || replayed.Result.SendAttempts != 1 {
			t.Fatal("framed stable replay changed acceptance", replayed)
		}
	}
	request := core.Request{Version: 1, RequestID: "framed-history", Operation: core.HistoryRead, Target: room, Limit: 1, Start: "retained"}
	for i, seen := range observed {
		result := call("framed-page", request, "")
		page := result.Page
		status := "more"
		if i == len(observed)-1 {
			status = "ok"
		}
		if page == nil || len(page.Messages) != 1 || page.Messages[0].ID != seen.ID || page.Messages[0].Message != seen.Message || page.Messages[0].From != seen.From || page.Messages[0].Target != seen.Target || page.Status != status || page.Cursor == "" || page.Gap {
			t.Fatal("framed bounded page mismatch", page)
		}
		request.Start, request.Cursor = "", page.Cursor
	}
	empty := call("framed-caught-up", request, "").Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("framed cursor replayed or lost position", empty)
	}
}
