//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSTOMPSignedAuthorityMetadataAndSizeRefusals(t *testing.T) {
	f := prepareSTOMPFixture(t)
	f.config.Principals[0].Targets = append(f.config.Principals[0].Targets, core.Target{Kind: "room", Name: "#other"})
	f.start(t)
	for i, kind := range []string{"signature", "expired", "target", "operation", "publisher", "reply", "content_type", "transaction", "size"} {
		t.Run(kind, func(t *testing.T) {
			peer := f.connect(t)
			r := core.Request{Version: 1, RequestID: fmt.Sprintf("stomp-refused-%d", i), Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
			if kind == "target" {
				r.Target.Name = "#other"
			}
			if kind == "operation" {
				r.Operation, r.Target, r.Message, r.LookupID = core.ReceiptGet, nil, "", "other"
			}
			token := f.credentials.token(t, r, false)
			if kind == "signature" {
				parts := strings.Split(token, ".")
				first := "A"
				if parts[2][0] == 'A' {
					first = "B"
				}
				parts[2] = first + parts[2][1:]
				token = strings.Join(parts, ".")
			}
			if kind == "expired" {
				now := time.Now().Unix()
				token = f.credentials.validityToken(t, r, now-400, now-280, false)
			}
			headers := []string{"destination:request", "content-type:application/jose", "receipt:refused"}
			switch kind {
			case "publisher":
				headers = append(headers, "user-id:other")
			case "reply":
				headers = append(headers, "reply-to:other")
			case "content_type":
				headers[1] = "content-type:application/json"
			case "transaction":
				headers = append(headers, "transaction:unsupported")
			case "size":
				token = strings.Repeat("a", 32769)
			}
			if err := peer.conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if kind == "size" {
				// A framing refusal may close while this oversized body is written.
				_, _ = peer.conn.Write([]byte("SEND\ndestination:request\ncontent-length:32769\n\n" + token + "\x00"))
				sqClosed(t, peer.conn, peer.reader)
			} else {
				sqWrite(t, peer.conn, "SEND", headers, []byte(token))
				if frame := peer.read(t); frame.command != "ERROR" {
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
