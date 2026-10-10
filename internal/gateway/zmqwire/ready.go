package zmqwire

import (
	"encoding/binary"
	"io"
	"strings"
)

func Ready() []byte {
	// Command name, property name, signed 32-bit property length, opaque value.
	body := append([]byte{5}, "READY"...)
	body = append(body, 11)
	body = append(body, "Socket-Type"...)
	body = binary.BigEndian.AppendUint32(body, 3)
	body = append(body, "REP"...)
	wire, _ := EncodeFrame(Frame{Command: true, Body: body})
	return wire
}

// ReadReady accepts one complete READY with an exact REQ socket type. Property
// names are case insensitive and unique; other bounded values are ignored and
// cannot supply an AIRC identity, request ID, target or operation.
func ReadReady(r io.Reader) error {
	f, err := ReadFrame(r)
	if err != nil {
		return err
	}
	if !f.Command || len(f.Body) < 6 || string(f.Body[:6]) != "\x05READY" {
		return ErrFrame
	}
	rest := f.Body[6:]
	seen := make(map[string]bool)
	request := false
	for len(rest) > 0 {
		if len(seen) >= 32 {
			return ErrFrame
		}
		size := int(rest[0])
		if size == 0 || len(rest) < 1+size+4 {
			return ErrFrame
		}
		name := rest[1 : 1+size]
		for _, c := range name {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.+", rune(c))) {
				return ErrFrame
			}
		}
		key := strings.ToLower(string(name))
		if seen[key] {
			return ErrFrame
		}
		seen[key] = true
		rest = rest[1+size:]
		length := uint64(binary.BigEndian.Uint32(rest))
		rest = rest[4:]
		if length > uint64(len(rest)) {
			return ErrFrame
		}
		value := rest[:int(length)]
		rest = rest[int(length):]
		if key == "socket-type" {
			if string(value) != "REQ" {
				return ErrFrame
			}
			request = true
		}
	}
	if !request {
		return ErrFrame
	}
	return nil
}
