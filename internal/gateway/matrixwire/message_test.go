package matrixwire

import (
	"encoding/json"
	"strings"
	"testing"
)

const wholeMatrixMessage = `{"type":"m.room.message","sender":"@alice:example.org","event_id":"$OpaqueID","origin_server_ts":1791450000123,"content":{"msgtype":"m.text","body":"  café & 世界\nnext  ","format":"org.matrix.custom.html","formatted_body":"ignored"},"unsigned":{"age":999999999,"transaction_id":"client-chosen"}}`

func TestMatrixWholeMessageData(t *testing.T) {
	m, err := ParseMessage([]byte(wholeMatrixMessage), "!Room")
	if err != nil || m.Room != "!Room" || m.Author != "@alice:example.org" || m.ID != "$OpaqueID" || m.Body != "  café & 世界\nnext  " || m.OriginMilliseconds != 1791450000123 {
		t.Fatal(m, err)
	}
	explicit := strings.Replace(wholeMatrixMessage, `"type":`, `"room_id":"!Room","type":`, 1)
	if _, err = ParseMessage([]byte(explicit), "!Room"); err != nil {
		t.Fatal(err)
	}
	if _, err = ParseMessage([]byte(explicit), "!room"); err == nil {
		t.Fatal("room case mismatch accepted")
	}
}

func TestMatrixPlainEventRefusals(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"encrypted":      func(e map[string]any) { e["type"] = "m.room.encrypted" },
		"state":          func(e map[string]any) { e["state_key"] = "" },
		"redaction":      func(e map[string]any) { e["redacts"] = "$old" },
		"redacted":       func(e map[string]any) { e["unsigned"].(map[string]any)["redacted_because"] = nil },
		"unsigned null":  func(e map[string]any) { e["unsigned"] = nil },
		"sender alias":   func(e map[string]any) { e["Sender"] = e["sender"]; delete(e, "sender") },
		"bad author":     func(e map[string]any) { e["sender"] = "Alice" },
		"missing ID":     func(e map[string]any) { delete(e, "event_id") },
		"room alias":     func(e map[string]any) { e["room_id"] = "#room:example.org" },
		"null stamp":     func(e map[string]any) { e["origin_server_ts"] = nil },
		"negative stamp": func(e map[string]any) { e["origin_server_ts"] = -1 },
		"overflow stamp": func(e map[string]any) { e["origin_server_ts"] = int64(9007199254740992) },
		"fraction stamp": func(e map[string]any) { e["origin_server_ts"] = 1.5 },
		"notice":         func(e map[string]any) { e["content"].(map[string]any)["msgtype"] = "m.notice" },
		"edit":           func(e map[string]any) { e["content"].(map[string]any)["m.new_content"] = map[string]any{} },
		"relation": func(e map[string]any) {
			e["content"].(map[string]any)["m.relates_to"] = map[string]any{"rel_type": "m.replace"}
		},
		"blank":       func(e map[string]any) { e["content"].(map[string]any)["body"] = " \n\t" },
		"NUL":         func(e map[string]any) { e["content"].(map[string]any)["body"] = "a\x00b" },
		"nested body": func(e map[string]any) { e["content"].(map[string]any)["body"] = map[string]any{"text": "body"} },
	} {
		t.Run(name, func(t *testing.T) {
			var e map[string]any
			if err := json.Unmarshal([]byte(wholeMatrixMessage), &e); err != nil {
				t.Fatal(err)
			}
			mutate(e)
			raw, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ParseMessage(raw, "!Room"); err == nil {
				t.Fatal("invalid message accepted")
			}
		})
	}
	for _, raw := range []string{`null`, `[]`, wholeMatrixMessage + `{}`, strings.Replace(wholeMatrixMessage, `"type":`, `"type":"m.room.encrypted","type":`, 1), strings.Replace(wholeMatrixMessage, "café", `\ud800`, 1), strings.Repeat(" ", 65536) + wholeMatrixMessage} {
		if _, err := ParseMessage([]byte(raw), "!Room"); err == nil {
			t.Fatal("invalid framing accepted")
		}
	}
}
