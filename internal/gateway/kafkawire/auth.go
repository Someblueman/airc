package kafkawire

import (
	"bytes"
	"encoding/binary"
	"unicode/utf8"
)

// VersionsReply validates an ApiVersions v0 body against the complete selected
// profile. Unknown APIs are allowed, but duplicate or malformed entries are not.
func VersionsReply(body []byte) error {
	if len(body) < 6 || len(body) > MaxFrame || binary.BigEndian.Uint16(body) != 0 {
		return ErrFrame
	}
	count := int32(binary.BigEndian.Uint32(body[2:]))
	if count < 0 || count > 256 || len(body) != 6+int(count)*6 {
		return ErrFrame
	}
	required := map[int16]int16{18: 0, 17: 1, 36: 1, 3: 8, 2: 5, 1: 11, 0: 7, 23: 3}
	seen := make(map[int16]bool, int(count))
	for i := 0; i < int(count); i++ {
		entry := body[6+i*6:]
		key, min, max := int16(binary.BigEndian.Uint16(entry)), int16(binary.BigEndian.Uint16(entry[2:])), int16(binary.BigEndian.Uint16(entry[4:]))
		if key < 0 || min < 0 || max < min || seen[key] {
			return ErrFrame
		}
		seen[key] = true
		if version, ok := required[key]; ok {
			if version < min || version > max {
				return ErrFrame
			}
			delete(required, key)
		}
	}
	if len(required) != 0 {
		return ErrFrame
	}
	return nil
}

func PlainHandshake() []byte { return []byte{0, 5, 'P', 'L', 'A', 'I', 'N'} }

// PlainHandshakeReply accepts only a successful bounded v1 mechanism list
// containing PLAIN. No mechanism fallback is performed.
func PlainHandshakeReply(body []byte) error {
	if len(body) < 6 || len(body) > 4096 || binary.BigEndian.Uint16(body) != 0 {
		return ErrFrame
	}
	count := int32(binary.BigEndian.Uint32(body[2:]))
	if count < 1 || count > 32 {
		return ErrFrame
	}
	rest := body[6:]
	seen := map[string]bool{}
	for range int(count) {
		if len(rest) < 2 {
			return ErrFrame
		}
		size := int(int16(binary.BigEndian.Uint16(rest)))
		rest = rest[2:]
		if size < 1 || size > 64 || len(rest) < size {
			return ErrFrame
		}
		name := string(rest[:size])
		rest = rest[size:]
		for _, r := range name {
			if r < 33 || r > 126 {
				return ErrFrame
			}
		}
		if seen[name] {
			return ErrFrame
		}
		seen[name] = true
	}
	if len(rest) != 0 || !seen["PLAIN"] {
		return ErrFrame
	}
	return nil
}

// PlainAuthenticate emits a SaslAuthenticate v1 body. The owner must use verified
// TLS, clear this owned credential buffer after writing, and never log it.
func PlainAuthenticate(username string, password []byte) ([]byte, error) {
	if len(username) < 1 || len(username) > 255 || !utf8.ValidString(username) || bytes.IndexByte([]byte(username), 0) >= 0 || len(password) < 1 || len(password) > 4096 || !utf8.Valid(password) || bytes.IndexByte(password, 0) >= 0 {
		return nil, ErrFrame
	}
	body := make([]byte, 4+2+len(username)+len(password))
	binary.BigEndian.PutUint32(body, uint32(len(body)-4))
	copy(body[5:], username)
	copy(body[6+len(username):], password)
	return body, nil
}

// PlainAuthenticateReply validates a successful v1 PLAIN response. Lifetime 0
// means re-authentication is not requested. A positive lifetime is returned to
// the owner, which must reconnect before expiry; broker text is never exposed.
func PlainAuthenticateReply(body []byte) (int64, error) {
	if len(body) < 16 || len(body) > 4096 || binary.BigEndian.Uint16(body) != 0 {
		return 0, ErrFrame
	}
	size := int(int16(binary.BigEndian.Uint16(body[2:])))
	if size < -1 || size > 1024 {
		return 0, ErrFrame
	}
	index := 4
	if size >= 0 {
		index += size
	}
	if len(body) != index+12 || binary.BigEndian.Uint32(body[index:]) != 0 {
		return 0, ErrFrame
	}
	lifetime := int64(binary.BigEndian.Uint64(body[index+4:]))
	if lifetime < 0 {
		return 0, ErrFrame
	}
	return lifetime, nil
}
