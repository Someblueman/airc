package snmpwire

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"hash"
)

const HMACSHA256192 = "hmac_sha256_192"
const HMACSHA196 = "hmac_sha1_96"

// Verifier snapshots an explicitly selected USM localized authentication key.
// There is no protocol fallback, password discovery or AIRC authorization here.
type Verifier struct {
	key    []byte
	digest func() hash.Hash
	size   int
}

func NewVerifier(protocol string, localizedKey []byte) (*Verifier, error) {
	v := &Verifier{}
	switch protocol {
	case HMACSHA256192:
		v.digest, v.size = sha256.New, 24
		if len(localizedKey) != 32 {
			return nil, ErrPacket
		}
	case HMACSHA196:
		v.digest, v.size = sha1.New, 12
		if len(localizedKey) != 20 {
			return nil, ErrPacket
		}
	default:
		return nil, ErrPacket
	}
	v.key = bytes.Clone(localizedKey)
	return v, nil
}
func (v *Verifier) Verify(raw []byte) (Packet, error) {
	if v == nil || v.digest == nil || len(v.key) == 0 {
		return Packet{}, ErrPacket
	}
	p, err := Parse(raw)
	if err != nil || p.authenticationEnd-p.authenticationStart != v.size {
		return Packet{}, ErrPacket
	}
	whole := bytes.Clone(p.Raw)
	clear(whole[p.authenticationStart:p.authenticationEnd])
	mac := hmac.New(v.digest, v.key)
	_, _ = mac.Write(whole)
	if !hmac.Equal(mac.Sum(nil)[:v.size], p.Raw[p.authenticationStart:p.authenticationEnd]) {
		return Packet{}, ErrPacket
	}
	return p, nil
}
