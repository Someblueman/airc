//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestAMQPPlainRouteMetadataAndSizeRefusals(t *testing.T) {
	f := prepareAMQPFixture(t)
	f.start(t)
	for i, kind := range []string{"malformed", "target", "operation", "reply", "content_type", "content_encoding", "mandatory_annotation", "size"} {
		t.Run(kind, func(t *testing.T) {
			peer := f.connect(t)
			r := core.Request{Version: 1, RequestID: fmt.Sprintf("amqp-refused-%d", i), Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
			condition := "amqp:unauthorized-access"
			switch kind {
			case "target":
				r.Target = &core.Target{Kind: "room", Name: "#other"}
			case "operation":
				r.Operation = core.ReceiptGet
				r.Target = nil
				r.Message = ""
				r.LookupID = "other"
			}
			body := amqpPlainBody(t, r)
			switch kind {
			case "malformed":
				body = body[:len(body)-1]
				condition = "amqp:decode-error"
			case "size":
				body = []byte(strings.Repeat("a", 32769))
				condition = "amqp:invalid-field"
			}
			message := aqDescribed(0x75, aqBytes(0xb0, body))
			switch kind {
			case "reply":
				message = append(aqDescribed(0x73, aqList([]byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, aqBytes(0xb1, []byte("other")))), message...)
			case "content_type":
				message = append(aqDescribed(0x73, aqList([]byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, aqBytes(0xb3, []byte("application/jose")))), message...)
				condition = "amqp:not-implemented"
			case "content_encoding":
				message = append(aqDescribed(0x73, aqList([]byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, aqBytes(0xb3, []byte("gzip")))), message...)
				condition = "amqp:not-implemented"
			case "mandatory_annotation":
				values := append(aqBytes(0xb3, []byte("required")), []byte{0x41}...)
				// map8: one symbol/boolean pair, count includes both values.
				annotation := append([]byte{0xc1, byte(len(values) + 1), 2}, values...)
				message = append(aqDescribed(0x72, annotation), message...)
				condition = "amqp:not-implemented"
			}
			if kind == "target" || kind == "operation" {
				// A request outside the route is rejected with a correlated
				// forbidden result; the connection stays usable.
				peer.flow(t)
				id := peer.submitMessage(t, message)
				result := peer.result(t, r.RequestID, id, 0x25)
				if result.Error == nil || result.Error.Code != core.Forbidden {
					t.Fatal(result)
				}
				aqWrite(t, peer.conn, aqFrame(0x18, 0, nil, nil))
				noMailboxJob(t, f.fileMailboxFixture, r.RequestID)
				return
			}
			peer.submitMessage(t, message)
			detach := aqRead(t, peer.conn)
			if detach.kind != 0x16 || aqField(detach, 1).number != 1 || len(aqField(detach, 2).fields) != 1 || string(aqField(detach, 2).fields[0].data) != condition {
				t.Fatal(detach)
			}
			if close := aqRead(t, peer.conn); close.kind != 0x18 {
				t.Fatal(close)
			}
			aqWrite(t, peer.conn, aqFrame(0x18, 0, nil, nil))
			noMailboxJob(t, f.fileMailboxFixture, r.RequestID)
		})
	}
	f.stop(t)
	if messages := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatal("refused AMQP intent reached native", messages)
	}
}
