package syslogwire

import (
	"bytes"
	//lint:ignore SA1019 RFC 5848 defines DSA; this verification-only profile refuses 1024-bit keys.
	"crypto/dsa"
	"crypto/sha256"
	"encoding/binary"
	"math/big"
)

// BlockVerifier holds a private snapshot of a provisioned DSA public key.
// A valid signature proves possession only; caller-owned grants and durable
// freshness/replay state must authorize a verified block before dispatch.
type BlockVerifier struct{ key dsa.PublicKey }

func NewBlockVerifier(key *dsa.PublicKey) (*BlockVerifier, error) {
	if key == nil || key.P == nil || key.Q == nil || key.G == nil || key.Y == nil {
		return nil, ErrSignedBlock
	}
	pbits, qbits := key.P.BitLen(), key.Q.BitLen()
	if !((pbits == 2048 && (qbits == 224 || qbits == 256)) || (pbits == 3072 && qbits == 256)) {
		return nil, ErrSignedBlock
	}
	v := &BlockVerifier{key: dsa.PublicKey{Parameters: dsa.Parameters{P: new(big.Int).Set(key.P), Q: new(big.Int).Set(key.Q), G: new(big.Int).Set(key.G)}, Y: new(big.Int).Set(key.Y)}}
	p, q, g, y := v.key.P, v.key.Q, v.key.G, v.key.Y
	one := big.NewInt(1)
	if p.Sign() <= 0 || q.Sign() <= 0 || g.Cmp(one) <= 0 || g.Cmp(p) >= 0 || y.Cmp(one) <= 0 || y.Cmp(p) >= 0 || !p.ProbablyPrime(32) || !q.ProbablyPrime(32) {
		return nil, ErrSignedBlock
	}
	if new(big.Int).Mod(new(big.Int).Sub(p, one), q).Sign() != 0 || new(big.Int).Exp(g, q, p).Cmp(one) != 0 || new(big.Int).Exp(y, q, p).Cmp(one) != 0 {
		return nil, ErrSignedBlock
	}
	return v, nil
}

// Verify requires the complete ordered covered-record list for a signature
// block. Certificate blocks take no covered records. No partial list succeeds.
func (v *BlockVerifier) Verify(raw []byte, covered [][]byte) (SignedBlock, error) {
	if v == nil || v.key.Q == nil {
		return SignedBlock{}, ErrSignedBlock
	}
	b, err := ParseSignedBlock(raw)
	if err != nil {
		return SignedBlock{}, err
	}
	r, rest, ok := signatureMPI(b.Signature, v.key.Q.BitLen())
	if !ok {
		return SignedBlock{}, ErrSignedBlock
	}
	s, rest, ok := signatureMPI(rest, v.key.Q.BitLen())
	if !ok || len(rest) != 0 {
		return SignedBlock{}, ErrSignedBlock
	}
	digest := sha256.Sum256(b.Signed)
	// DSA truncates the digest to the subgroup's byte length (RFC 4880).
	if !dsa.Verify(&v.key, digest[:v.key.Q.BitLen()/8], r, s) {
		return SignedBlock{}, ErrSignedBlock
	}
	if b.Certificate {
		if len(covered) != 0 {
			return SignedBlock{}, ErrSignedBlock
		}
	} else {
		if len(covered) != len(b.Hashes) {
			return SignedBlock{}, ErrSignedBlock
		}
		for i, rawRecord := range covered {
			if _, err := Parse(rawRecord); err != nil {
				return SignedBlock{}, ErrSignedBlock
			}
			h := sha256.Sum256(rawRecord)
			if !bytes.Equal(h[:], b.Hashes[i]) {
				return SignedBlock{}, ErrSignedBlock
			}
		}
	}
	return b, nil
}

func signatureMPI(raw []byte, maxBits int) (*big.Int, []byte, bool) {
	if len(raw) < 2 {
		return nil, nil, false
	}
	bits := int(binary.BigEndian.Uint16(raw[:2]))
	n := (bits + 7) / 8
	if bits == 0 || bits > maxBits || len(raw) < 2+n {
		return nil, nil, false
	}
	x := new(big.Int).SetBytes(raw[2 : 2+n])
	if x.BitLen() != bits {
		return nil, nil, false
	}
	return x, raw[2+n:], true
}
