package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

// Decode rejects duplicate keys, case aliases, unknown fields, trailing values,
// and excessive nesting before interpreting the request. Bound the input at
// the transport too; this function cannot undo an unbounded prior allocation.
func Decode(data []byte) (Request, error) {
	var r Request
	if len(data) == 0 || len(data) > MaxEnvelopeBytes || !utf8.Valid(data) {
		return r, errors.New("invalid envelope size or UTF-8")
	}
	if err := unicodeEscapes(data); err != nil {
		return r, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := value(d, 0); err != nil {
		return r, err
	}
	if _, err := d.Token(); err != io.EOF {
		return r, errors.New("trailing envelope data")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return r, errors.New("request must be an object")
	}
	allowed := map[string]bool{"version": true, "request_id": true, "operation": true, "target": true, "message": true, "parent_id": true, "lookup_id": true, "cursor": true, "query": true, "limit": true, "issued_at": true, "expires_at": true, "delivery_policy": true}
	for key, raw := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return r, errors.New("unknown or null request field")
		}
	}
	if raw, ok := fields["target"]; ok {
		var target map[string]json.RawMessage
		if err := json.Unmarshal(raw, &target); err != nil || len(target) != 2 {
			return r, errors.New("invalid target fields")
		}
		for k, v := range target {
			if (k != "kind" && k != "name") || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return r, errors.New("invalid target field")
			}
		}
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	return r, r.Normalize()
}

// encoding/json substitutes U+FFFD for unpaired UTF-16 surrogate escapes.
// Reject those instead of silently changing authenticated message content.
func unicodeEscapes(data []byte) error {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) || data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return errors.New("incomplete Unicode escape")
		}
		n, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return errors.New("invalid Unicode escape")
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return errors.New("unpaired Unicode surrogate")
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return errors.New("unpaired Unicode surrogate")
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return errors.New("unpaired Unicode surrogate")
		}
		i += 6
	}
	return nil
}

func value(d *json.Decoder, depth int) error {
	if depth > 8 {
		return errors.New("envelope nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate or invalid JSON key")
			}
			seen[name] = true
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}
