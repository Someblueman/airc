// Package jsoninput enforces the shared strict JSON boundary for fixed gateway schemas.
package jsoninput

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Decode bounds input and rejects duplicate, unknown, case-aliased and null
// fields. dst must point to a fixed struct schema with explicitly tagged fields.
func Decode(data []byte, dst any, maxBytes int) error {
	return DecodeDepth(data, dst, maxBytes, 8)
}

// DecodeDepth applies the same strict schema boundary with a caller-selected
// nesting bound for deeper owner configuration schemas.
func DecodeDepth(data []byte, dst any, maxBytes, maxDepth int) error {
	if maxDepth < 1 || maxDepth > 16 {
		return errors.New("invalid JSON nesting bound")
	}
	if err := validateDepth(data, maxBytes, maxDepth); err != nil {
		return err
	}
	t := reflect.TypeOf(dst)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(dst).IsNil() || t.Elem().Kind() != reflect.Struct {
		return errors.New("JSON destination must point to a struct")
	}
	if err := fields(data, t.Elem()); err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

// Validate checks the shared bounded JSON syntax used at gateway protocol
// boundaries. It deliberately permits a top-level scalar or null; callers that
// require a fixed object apply Decode's schema checks afterward.
func Validate(data []byte, maxBytes int) error {
	return validateDepth(data, maxBytes, 8)
}

func validateDepth(data []byte, maxBytes, maxDepth int) error {
	if len(data) == 0 || len(data) > maxBytes || !utf8.Valid(data) {
		return errors.New("invalid JSON size or UTF-8")
	}
	if err := unicodeEscapes(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := value(d, 0, maxDepth); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func fields(data json.RawMessage, t reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("null JSON value")
	}
	if t.Kind() == reflect.Pointer {
		return fields(data, t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil || object == nil {
			return errors.New("expected JSON object")
		}
		schema := make(map[string]reflect.Type, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if f.PkgPath != "" || name == "" || name == "-" {
				continue
			}
			schema[name] = f.Type
		}
		for name, raw := range object {
			child, ok := schema[name]
			if !ok {
				return fmt.Errorf("unknown JSON field %q", name)
			}
			if err := fields(raw, child); err != nil {
				return fmt.Errorf("field %s: %w", name, err)
			}
		}
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return errors.New("expected JSON array")
		}
		for _, item := range items {
			if err := fields(item, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
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

func value(d *json.Decoder, depth, maxDepth int) error {
	if depth > maxDepth {
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
			if err := value(d, depth+1, maxDepth); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := value(d, depth+1, maxDepth); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}
