package pgnotifywire

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"time"
)

func readFrame(conn net.Conn, deadline time.Time) (byte, []byte, error) {
	if err := conn.SetReadDeadline(deadline); err != nil {
		return 0, nil, err
	}
	var typ [1]byte
	if _, err := readFull(conn, typ[:]); err != nil {
		return 0, nil, err
	}
	var size [4]byte
	if _, err := readFull(conn, size[:]); err != nil {
		return 0, nil, err
	}
	length := int(binary.BigEndian.Uint32(size[:]))
	if length < 4 || length > maxFrameBytes {
		return 0, nil, ErrProtocol
	}
	body := make([]byte, length-4)
	if _, err := readFull(conn, body); err != nil {
		clear(body)
		return 0, nil, err
	}
	return typ[0], body, nil
}

func readAsyncFrame(conn net.Conn) (byte, []byte, error) {
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return 0, nil, err
	}
	var typ [1]byte
	if _, err := readFull(conn, typ[:]); err != nil {
		return 0, nil, err
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, nil, err
	}
	var size [4]byte
	if _, err := readFull(conn, size[:]); err != nil {
		return 0, nil, err
	}
	length := int(binary.BigEndian.Uint32(size[:]))
	if length < 4 || length > maxFrameBytes {
		return 0, nil, ErrProtocol
	}
	body := make([]byte, length-4)
	if _, err := readFull(conn, body); err != nil {
		clear(body)
		return 0, nil, err
	}
	return typ[0], body, nil
}

func (s *Session) parameterStatus(body []byte) error {
	key, rest, ok := takeCString(body, 128)
	if !ok || key == "" || !printableASCII(key) {
		return ErrProtocol
	}
	value, rest, ok := takeCString(rest, 1024)
	if !ok || !printableASCII(value) || len(rest) != 0 {
		return ErrProtocol
	}
	for existing, previous := range s.parameters {
		if strings.EqualFold(existing, key) {
			if existing != key || previous != value || key == "client_encoding" {
				return ErrProtocol
			}
		}
	}
	_, exists := s.parameters[key]
	if !exists && len(s.parameters) >= 32 {
		return ErrProtocol
	}
	s.parameters[key] = value
	return nil
}

func (s *Session) notice(body []byte) error {
	fields := 0
	for len(body) > 0 {
		if body[0] == 0 {
			if len(body) == 1 {
				return nil
			}
			return ErrProtocol
		}
		if fields >= 32 {
			return ErrProtocol
		}
		body = body[1:]
		_, rest, ok := takeCString(body, 2048)
		if !ok {
			return ErrProtocol
		}
		body = rest
		fields++
	}
	return ErrProtocol
}

func (s *Session) notification(body []byte) ([]byte, error) {
	if len(body) < 5 || binary.BigEndian.Uint32(body[:4]) == 0 {
		return nil, ErrProtocol
	}
	channel, rest, ok := takeCString(body[4:], 128)
	if !ok || channel != s.channel {
		return nil, ErrProtocol
	}
	payload, rest, ok := takeCString(rest, 7999)
	if !ok || len(payload) == 0 || len(rest) != 0 || !printableASCII(payload) {
		return nil, ErrProtocol
	}
	return []byte(payload), nil
}

func takeCString(body []byte, maximum int) (string, []byte, bool) {
	if len(body) == 0 {
		return "", nil, false
	}
	end := bytes.IndexByte(body, 0)
	if end < 0 || end > maximum {
		return "", nil, false
	}
	return string(body[:end]), body[end+1:], true
}

func printableASCII(value string) bool {
	for i := range value {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
