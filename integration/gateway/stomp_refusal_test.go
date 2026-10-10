//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSTOMPPlainAuthorityMetadataAndSizeRefusals(t *testing.T) {
	f := prepareSTOMPFixture(t)
	f.config.Principals[0].Targets = append(f.config.Principals[0].Targets, core.Target{Kind: "room", Name: "#other"})
	f.start(t)
	for i, kind := range []string{"malformed", "envelope", "target", "operation", "publisher", "reply", "content_type", "transaction", "size"} {
		t.Run(kind, func(t *testing.T) {
			peer := f.connect(t)
			r := core.Request{Version: 1, RequestID: fmt.Sprintf("stomp-refused-%d", i), Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
			if kind == "target" {
				r.Target.Name = "#other"
			}
			if kind == "operation" {
				r.Operation, r.Target, r.Message, r.LookupID = core.ReceiptGet, nil, "", "other"
			}
			body := stompPlainBody(t, r)
			headers := []string{"destination:request", "content-type:application/json", "receipt:refused"}
			message := "unauthorized STOMP request"
			switch kind {
			case "malformed":
				body = body[:len(body)-1]
				message = "invalid STOMP request body"
			case "envelope":
				// A compact JWS is no longer a request: only the connection authorizes.
				body = []byte(f.credentials.token(t, r, false))
				message = "invalid STOMP request body"
			case "publisher":
				headers = append(headers, "user-id:other")
				message = "unauthorized STOMP request metadata"
			case "reply":
				headers = append(headers, "reply-to:other")
				message = "unauthorized STOMP request metadata"
			case "content_type":
				headers[1] = "content-type:application/jose"
				message = "unsupported STOMP content type"
			case "transaction":
				headers = append(headers, "transaction:unsupported")
				message = "unsupported STOMP request header"
			case "size":
				body = []byte(strings.Repeat("a", 32769))
			}
			if err := peer.conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if kind == "size" {
				// A framing refusal may close while this oversized body is written.
				_, _ = peer.conn.Write([]byte("SEND\ndestination:request\ncontent-length:32769\n\n" + string(body) + "\x00"))
				sqClosed(t, peer.conn, peer.reader)
			} else {
				sqWrite(t, peer.conn, "SEND", headers, body)
				if frame := peer.read(t); frame.command != "ERROR" || frame.headers["message"] != message {
					t.Fatal(frame)
				}
				sqClosed(t, peer.conn, peer.reader)
			}
			noMailboxJob(t, f.fileMailboxFixture, r.RequestID)
		})
	}
	f.stop(t)
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatal("refused STOMP intent reached native", messages)
	}
}
