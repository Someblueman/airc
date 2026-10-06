// Package bpv7 decodes a bounded one-way RFC 9171 application ingress profile.
// It does not implement routing, fragmentation, status reports or BPSec.
package bpv7

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
)

const MaxBytes = 64 << 10
const MaxPayloadBytes = 32768
const EpochSeconds int64 = 946684800
const epochMilliseconds = uint64(EpochSeconds) * 1000

var ErrBundle = errors.New("invalid or unsupported BPv7 ingress bundle")

type Bundle struct {
	Source, Destination                                  string
	CreationMilliseconds, Sequence, LifetimeMilliseconds uint64
	Payload                                              string
}

// RequestID binds the RFC bundle identity without changing when delivery is
// delayed. Changing content under that identity conflicts in the existing journal.
func (b Bundle) RequestID() string {
	data := append([]byte("airc-bpv7-identity-v1\x00"), b.Source...)
	data = append(data, 0)
	data = binary.BigEndian.AppendUint64(data, b.CreationMilliseconds)
	data = binary.BigEndian.AppendUint64(data, b.Sequence)
	digest := sha256.Sum256(data)
	return "bp7-" + base64.RawURLEncoding.EncodeToString(digest[:])
}

func (b Bundle) IssuedAt() int64 { return EpochSeconds + int64(b.CreationMilliseconds/1000) }
func (b Bundle) ExpiresAt() int64 {
	return EpochSeconds + int64((b.CreationMilliseconds+b.LifetimeMilliseconds)/1000)
}

func Decode(data []byte) (Bundle, error) {
	if len(data) == 0 || len(data) > MaxBytes {
		return Bundle{}, ErrBundle
	}
	r := reader{data: data}
	// RFC 9171 permits the standard self-described CBOR tag on the outer array.
	if len(data) >= 3 && string(data[:3]) == "\xd9\xd9\xf7" {
		r.pos = 3
	}
	if r.take() != 0x9f {
		return Bundle{}, ErrBundle
	}
	start := r.pos
	indefinite := r.array(9)
	if r.head(0) != 7 {
		r.err = ErrBundle
	}
	flags := r.head(0)
	if flags & ^uint64(4) != 0 {
		r.err = ErrBundle
	}
	kind := r.head(0)
	if kind != 1 && kind != 2 {
		r.err = ErrBundle
	}
	b := Bundle{Destination: r.endpoint(false), Source: r.endpoint(true)}
	r.nullEndpoint()
	timestamp := r.array(2)
	b.CreationMilliseconds, b.Sequence = r.head(0), r.head(0)
	r.end(timestamp)
	b.LifetimeMilliseconds = r.head(0)
	r.crc(start, kind, indefinite)
	if r.err != nil || b.CreationMilliseconds == 0 || b.LifetimeMilliseconds == 0 || b.CreationMilliseconds > math.MaxInt64-epochMilliseconds || b.LifetimeMilliseconds > math.MaxInt64-epochMilliseconds-b.CreationMilliseconds {
		return Bundle{}, ErrBundle
	}
	start = r.pos
	// Payload is the only canonical block. Both definite and indefinite block
	// arrays are accepted, but the payload byte string must be definite-length.
	var count uint64 = 6
	if r.pos < len(data) && data[r.pos] == 0x85 {
		count = 5
	}
	indefinite = r.array(count)
	blockType, blockNumber, blockFlags := r.head(0), r.head(0), r.head(0)
	if blockType != 1 || blockNumber != 1 || blockFlags != 0 {
		r.err = ErrBundle
	}
	kind = r.head(0)
	if kind > 2 || !indefinite && ((kind == 0 && count != 5) || (kind != 0 && count != 6)) {
		r.err = ErrBundle
	}
	payload := r.bytes(2, MaxPayloadBytes)
	if len(payload) == 0 {
		r.err = ErrBundle
	}
	if kind == 0 {
		r.end(indefinite)
	} else {
		r.crc(start, kind, indefinite)
	}
	if r.take() != 0xff || r.err != nil || r.pos != len(data) {
		return Bundle{}, ErrBundle
	}
	b.Payload = string(payload)
	return b, nil
}
