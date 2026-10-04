//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestFramedCLISharedOperationsAndMultiplexedWatch(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint(secure), func(t *testing.T) {
			conn, credentials, stop := framedFixture(t, secure)
			profile := "encrypted"
			if secure {
				profile = "signed"
			}
			sequence := 0
			tokens := map[string]string{}
			call := func(id string, r core.Request) adapter.FrameResponse {
				sequence++
				id += fmt.Sprint("-", sequence)
				token := credentials.token(t, r, profile == "encrypted")
				if r.Operation == core.Send || r.Operation == core.Reply {
					if saved := tokens[r.RequestID]; saved != "" {
						token = saved
					} else {
						tokens[r.RequestID] = token
					}
				}
				writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: id, Profile: profile, Envelope: token})
				response := readFrame(t, conn)
				if response.CorrelationID != id || response.Event != "result" || response.Error != nil || response.Result == nil {
					t.Fatal("operation failed", response)
				}
				return response
			}
			discovery := call("discover", core.Request{Version: 1, RequestID: "discover", Operation: core.CapabilitiesGet})
			caps := discovery.Result.Capabilities
			if caps == nil || len(caps.Operations) != 7 || strings.Join(caps.WatchModes, ",") != "long_poll,framed_stream" {
				t.Fatal("wrong framed capabilities", caps)
			}
			if secure {
				writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "tls-encrypted", Profile: "encrypted", Envelope: credentials.token(t, core.Request{Version: 1, RequestID: "tls-encrypted", Operation: core.CapabilitiesGet}, true)})
				if r := readFrame(t, conn); r.Result == nil || r.Result.Capabilities == nil {
					t.Fatal("TLS encrypted profile failed", r)
				}
			}
			target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
			send := core.Request{Version: 1, RequestID: "stream-send", Operation: core.Send, Target: target, Message: "TCP/TLS whole body\n🙂"}
			admitted := call("send", send)
			if admitted.Result.Result == nil || !admitted.Result.Result.Durable {
				t.Fatal("not durable", admitted)
			}
			var messageID string
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
				r := call("receipt", core.Request{Version: 1, RequestID: "receipt", Operation: core.ReceiptGet, LookupID: send.RequestID})
				if r.Result.Result.State == core.Accepted {
					messageID = r.Result.Result.MessageID
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if messageID == "" {
				t.Fatal("no actual native acceptance")
			}
			replay := call("replay", send)
			if replay.Result.Result.MessageID != messageID || replay.Result.Result.SendAttempts != 1 {
				t.Fatal("replay redispatched", replay)
			}
			reply := send
			reply.RequestID = "stream-reply"
			reply.Operation = core.Reply
			reply.ParentID = messageID
			reply.Message = "framed reply"
			_ = call("reply", reply)
			for deadline := time.Now().Add(5 * time.Second); ; {
				r := call("reply-status", core.Request{Version: 1, RequestID: "reply-status", Operation: core.ReceiptGet, LookupID: reply.RequestID})
				if r.Result.Result.State == core.Accepted {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("reply not accepted", r)
				}
				time.Sleep(20 * time.Millisecond)
			}
			history := call("history", core.Request{Version: 1, RequestID: "history", Operation: core.HistoryRead, Target: target, Limit: 10, Start: "retained"})
			if history.Result.Page == nil || len(history.Result.Page.Messages) < 1 || history.Result.Page.Messages[0].Message != send.Message || history.Result.Page.Messages[0].From != "fixture-author" {
				t.Fatal("native history lost identity/body", history)
			}
			search := call("search", core.Request{Version: 1, RequestID: "search", Operation: core.Search, Target: target, Limit: 10, Query: "whole body"})
			if search.Result.Page == nil || len(search.Result.Page.Messages) != 1 || search.Result.Page.Messages[0].ID != messageID {
				t.Fatal("search mismatch", search)
			}
			directory := call("directory", core.Request{Version: 1, RequestID: "directory", Operation: core.Directory, Limit: 1, Query: "fixture-author"})
			if directory.Result.Directory == nil {
				t.Fatal("missing public directory")
			}
			watch := core.Request{Version: 1, RequestID: "watch", Operation: core.Watch, Target: target, Limit: 10}
			_ = call("poll", watch)
			writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "watch", Profile: profile, Envelope: credentials.validityToken(t, watch, time.Now().Unix()-85, time.Now().Unix()-55, profile == "encrypted"), Stream: true})
			first := readFrame(t, conn)
			if first.CorrelationID != "watch" || first.Page == nil {
				t.Fatal("missing stream catchup", first)
			}
			send.RequestID = "stream-live"
			send.Message = "send while watch is active"
			writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "live", Profile: profile, Envelope: credentials.token(t, send, profile == "encrypted")})
			live, admission := false, false
			var cursor string
			for !live || !admission {
				response := readFrame(t, conn)
				if response.CorrelationID == "live" && response.Result != nil {
					admission = response.Result.Result.Durable
				}
				if response.CorrelationID == "watch" && response.Page != nil {
					for _, m := range response.Page.Messages {
						if m.Message == send.Message {
							live = true
							cursor = response.Page.Cursor
						}
					}
				}
				if response.Error != nil || response.Event == "end" {
					t.Fatal("multiplex failed", response)
				}
			}
			if !strings.HasPrefix(cursor, "gc1.") {
				t.Fatal("missing scoped stream cursor", cursor)
			}
			for {
				terminal := readFrame(t, conn)
				if terminal.Event == "end" {
					if terminal.CorrelationID != "watch" || terminal.Reason != "lease_expired" {
						t.Fatal("signed watch deadline escaped", terminal)
					}
					break
				}
				if terminal.CorrelationID != "watch" || terminal.Page == nil {
					t.Fatal("unexpected stream continuation", terminal)
				}
			}
			watch.Cursor = cursor
			writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: "renewed", Profile: profile, Envelope: credentials.token(t, watch, profile == "encrypted"), Stream: true})
			renewed := readFrame(t, conn)
			if renewed.CorrelationID != "renewed" || renewed.Page == nil || len(renewed.Page.Messages) != 0 {
				t.Fatalf("signed cursor replayed delivered message: %+v page=%+v", renewed, renewed.Page)
			}
			// Stop with an active native watch: service must close transport and join leases.
			stop()
		})
	}
}
