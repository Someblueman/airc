package matrixwire

import (
	"encoding/json"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const (
	ReadMarker          = "AIRC-READ/1"
	MaxReadRequestBytes = 2048
	readExtension       = "org.airc.gateway.read"
)

var ErrReadRequest = errors.New("invalid Matrix AIRC read request")

// ReadRequest is the bounded application intent carried beside an exact body
// marker in a normal m.text event. Matrix identities and event IDs are never
// taken from these fields.
type ReadRequest struct {
	CorrelationID string `json:"correlation_id"`
	Operation     string `json:"operation"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Query         string `json:"query,omitempty"`
	From          string `json:"from,omitempty"`
	ParentID      string `json:"parent_id,omitempty"`
	ThreadMode    string `json:"thread_mode,omitempty"`
}

type readContent struct {
	MsgType string      `json:"msgtype"`
	Body    string      `json:"body"`
	Read    ReadRequest `json:"org.airc.gateway.read"`
}

// ReadContent builds the exact Matrix m.text content for an AIRC read. Native
// target and event identity are supplied by the verified route and homeserver.
func ReadContent(request ReadRequest) ([]byte, error) {
	if !validCorrelationID(request.CorrelationID) || !validReadOperation(request.Operation) {
		return nil, ErrReadRequest
	}
	requestData, err := json.Marshal(request)
	if err != nil || len(requestData) > MaxReadRequestBytes || jsoninput.Validate(requestData, MaxReadRequestBytes) != nil {
		return nil, ErrReadRequest
	}
	content := readContent{MsgType: "m.text", Body: ReadMarker, Read: request}
	data, err := json.Marshal(content)
	if err != nil || len(data) > MaxReadRequestBytes+128 || jsoninput.Validate(data, MaxReadRequestBytes+128) != nil {
		return nil, ErrReadRequest
	}
	return data, nil
}

// ParseReadContent reports intent when either the exact marker or extension key
// is present. A lone or malformed signal is an error and must not fall back to
// ordinary Send handling.
func ParseReadContent(data []byte) (ReadRequest, bool, error) {
	if jsoninput.Validate(data, 65536) != nil {
		return ReadRequest{}, false, ErrReadRequest
	}
	var event map[string]json.RawMessage
	if json.Unmarshal(data, &event) != nil || event == nil {
		return ReadRequest{}, false, ErrReadRequest
	}
	var content map[string]json.RawMessage
	if json.Unmarshal(event["content"], &content) != nil || content == nil {
		return ReadRequest{}, false, ErrReadRequest
	}
	var body string
	bodyOK := json.Unmarshal(content["body"], &body) == nil
	var msgType string
	msgTypeOK := json.Unmarshal(content["msgtype"], &msgType) == nil
	_, hasExtension := content[readExtension]
	intent := bodyOK && body == ReadMarker || hasExtension
	if !intent {
		return ReadRequest{}, false, nil
	}
	if !msgTypeOK || msgType != "m.text" || !bodyOK || body != ReadMarker || !hasExtension {
		return ReadRequest{}, true, ErrReadRequest
	}
	var request ReadRequest
	if jsoninput.Decode(content[readExtension], &request, MaxReadRequestBytes) != nil ||
		!validCorrelationID(request.CorrelationID) || !validReadOperation(request.Operation) {
		return ReadRequest{}, true, ErrReadRequest
	}
	return request, true, nil
}

func validCorrelationID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validReadOperation(operation string) bool {
	return operation == "context.read" || operation == "history.read" || operation == "search"
}
