package core

import "github.com/Someblueman/airc/internal/gateway/jsoninput"

// Decode parses a bounded caller request. Parsing alone never establishes
// authentication, authorization, or durable gateway responsibility.
func Decode(data []byte) (Request, error) {
	var r Request
	if err := jsoninput.Decode(data, &r, MaxEnvelopeBytes); err != nil {
		return r, err
	}
	return r, r.Normalize()
}
