// Package framedbridge carries one bounded authenticated exchange to a TLS gateway.
package framedbridge

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const MaxRequest = 16384
const MaxResponse = 65536

var correlationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type frameRequest struct {
	Version       int    `json:"version"`
	CorrelationID string `json:"correlation_id"`
	Profile       string `json:"profile"`
	Envelope      string `json:"envelope"`
	Stream        bool   `json:"stream"`
}

// Request loads an original compact envelope and creates a framed-v1 request.
func Request(profile, correlation, input string) ([]byte, error) {
	if (profile != "signed" && profile != "encrypted") || !correlationPattern.MatchString(correlation) || !filepath.IsAbs(input) {
		return nil, errors.New("invalid envelope profile, correlation or path")
	}
	f, err := os.Open(input)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > MaxRequest {
		return nil, errors.New("envelope must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxRequest+1))
	if err != nil || len(data) > MaxRequest {
		return nil, errors.New("envelope read failed or exceeds bound")
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	if len(data) == 0 || bytes.IndexAny(data, " \r\n\t") >= 0 {
		return nil, errors.New("expected a compact signed or encrypted envelope")
	}
	request, err := json.Marshal(frameRequest{Version: 1, CorrelationID: correlation, Profile: profile, Envelope: string(data)})
	if err != nil || len(request) > MaxRequest {
		return nil, errors.New("complete request exceeds frame bound")
	}
	return request, nil
}

// Correlation rejects streaming or malformed wrappers before forwarding.
func Correlation(data []byte) (string, error) {
	var frame frameRequest
	if jsoninput.Decode(data, &frame, MaxRequest) != nil || frame.Version != 1 || frame.Stream || !correlationPattern.MatchString(frame.CorrelationID) || (frame.Profile != "signed" && frame.Profile != "encrypted") || frame.Envelope == "" {
		return "", errors.New("bridge requires one bounded nonstreaming framed request")
	}
	return frame.CorrelationID, nil
}

// ValidateResponse requires one complete correlated terminal JSON response.
func ValidateResponse(data []byte, correlation string) error {
	if err := jsoninput.ValidateDepth(data, MaxResponse, 16); err != nil {
		return errors.New("invalid gateway response JSON")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 4 || fields["version"] == nil || fields["correlation_id"] == nil || fields["event"] == nil {
		return errors.New("invalid terminal gateway response fields")
	}
	var response struct {
		Version       int    `json:"version"`
		CorrelationID string `json:"correlation_id"`
		Event         string `json:"event"`
	}
	if json.Unmarshal(data, &response) != nil || response.Version != 1 || response.CorrelationID != correlation || (response.Event != "result" && response.Event != "error") {
		return errors.New("invalid or uncorrelated gateway result")
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(fields[response.Event], &payload) != nil || payload == nil {
		return errors.New("gateway terminal payload must be an object")
	}
	return nil
}

// ReadFrame reads a complete length-prefixed record within max.
func ReadFrame(reader io.Reader, max uint32) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > max {
		return nil, errors.New("frame length exceeds bound")
	}
	data := make([]byte, n)
	_, err := io.ReadFull(reader, data)
	return data, err
}

// WriteFrame writes one complete length-prefixed record within max.
func WriteFrame(writer io.Writer, data []byte, max uint32) error {
	if len(data) == 0 || uint64(len(data)) > uint64(max) {
		return errors.New("frame length exceeds bound")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	wire := append(header[:], data...)
	for len(wire) > 0 {
		n, err := writer.Write(wire)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		wire = wire[n:]
	}
	return nil
}
