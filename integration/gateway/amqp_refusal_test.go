//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"github.com/Someblueman/airc/internal/gateway/core"
	"strings"
	"testing"
	"time"
)

func TestAMQPSignedAuthorityMetadataAndSizeRefusals(t *testing.T) {
	f := prepareAMQPFixture(t)
	f.config.Principals[0].Targets = append(f.config.Principals[0].Targets, core.Target{Kind: "room", Name: "#other"})
	f.start(t)
	for i, kind := range []string{"signature", "expired", "target", "operation", "publisher", "reply", "content_encoding", "mandatory_annotation", "size"} {
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
			token := f.credentials.token(t, r, false)
			if kind == "signature" {
				parts := strings.Split(token, ".")
				if parts[2][0] == 'A' {
					parts[2] = "B" + parts[2][1:]
				} else {
					parts[2] = "A" + parts[2][1:]
				}
				token = strings.Join(parts, ".")
			}
			if kind == "expired" {
				now := time.Now().Unix()
				token = f.credentials.validityToken(t, r, now-400, now-280, false)
			}
			if kind == "size" {
				token = strings.Repeat("a", 32769)
				condition = "amqp:invalid-field"
			}
			message := aqDescribed(0x75, aqBytes(0xb0, []byte(token)))
			switch kind {
			case "publisher":
				message = append(aqDescribed(0x73, aqList([]byte{0x40}, aqBytes(0xb0, []byte("other")))), message...)
			case "reply":
				message = append(aqDescribed(0x73, aqList([]byte{0x40}, []byte{0x40}, []byte{0x40}, []byte{0x40}, aqBytes(0xb1, []byte("other")))), message...)
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
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatal("refused AMQP intent reached native", messages)
	}
}
