package ircwire

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const ReadMarker = "AIRC-READ/1"
const MaxReadRequestBytes = 2048

// ReadRequest carries read parameters and a client correlation handle. The
// trusted event supplies identity and event ID; the fixed grant supplies target.
type ReadRequest struct {
	CorrelationID string         `json:"correlation_id"`
	Operation     core.Operation `json:"operation"`
	Cursor        string         `json:"cursor,omitempty"`
	Limit         int            `json:"limit,omitempty"`
	Query         string         `json:"query,omitempty"`
	From          string         `json:"from,omitempty"`
	ParentID      string         `json:"parent_id,omitempty"`
	ThreadMode    string         `json:"thread_mode,omitempty"`
}

func DecodeReadRequest(tag string) (ReadRequest, error) {
	if len(tag) == 0 || len(tag) > base64.RawURLEncoding.EncodedLen(MaxReadRequestBytes) {
		return ReadRequest{}, ErrLine
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(tag)
	if err != nil || base64.RawURLEncoding.EncodeToString(data) != tag {
		return ReadRequest{}, ErrLine
	}
	var r ReadRequest
	if jsoninput.Decode(data, &r, MaxReadRequestBytes) != nil || !readOperation(r.Operation) || !readCorrelationID(r.CorrelationID) {
		return ReadRequest{}, ErrLine
	}
	return r, nil
}

func readCorrelationID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func readOperation(op core.Operation) bool {
	return op == core.HistoryRead || op == core.Search || op == core.ContextRead
}

// ReadRequestLine is a complete client PRIVMSG. Parameters remain visible to
// the IRC room; only the resulting native content is encrypted on its return.
func ReadRequestLine(room string, r ReadRequest) ([]byte, error) {
	if len(room) < 2 || len(room) > 64 || room[0] != '#' {
		return nil, ErrLine
	}
	for _, c := range room[1:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return nil, ErrLine
		}
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > MaxReadRequestBytes || !readOperation(r.Operation) || !readCorrelationID(r.CorrelationID) {
		return nil, ErrLine
	}
	return clientTagLine("+airc/read", base64.RawURLEncoding.EncodeToString(data), "PRIVMSG", room, ReadMarker)
}

// Only one literal client-only tag is emitted; its values need no IRC escaping.
func clientTagLine(key, value, command, target, body string) ([]byte, error) {
	if key != "+airc/read" && key != "+airc/result" || value == "" || strings.ContainsAny(value, " ;\\\r\n\x00") {
		return nil, ErrLine
	}
	prefix := "@" + key + "=" + value + " "
	if len(prefix) > 4094 {
		return nil, ErrLine
	}
	line, err := Encode(command, []string{target}, &body)
	if err != nil {
		return nil, err
	}
	return append([]byte(prefix), line...), nil
}
