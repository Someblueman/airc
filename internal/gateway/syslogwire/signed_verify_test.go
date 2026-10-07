package syslogwire

import (
	//lint:ignore SA1019 Independent RFC 5848 test signer; never used for gateway credentials.
	"crypto/dsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math/big"
	"strings"
	"testing"
)

func signedFixtureKey(t *testing.T) *dsa.PrivateKey {
	t.Helper()
	k := new(dsa.PrivateKey)
	if err := dsa.GenerateParameters(&k.Parameters, rand.Reader, dsa.L2048N256); err != nil {
		t.Fatal(err)
	}
	if err := dsa.GenerateKey(k, rand.Reader); err != nil {
		t.Fatal(err)
	}
	return k
}

func fixtureMPI(x *big.Int) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(x.BitLen()))
	return append(b, x.Bytes()...)
}

func fixtureSign(t *testing.T, key *dsa.PrivateKey, unsigned string) []byte {
	t.Helper()
	h := sha256.Sum256([]byte(unsigned))
	r, s, err := dsa.Sign(rand.Reader, key, h[:key.Q.BitLen()/8])
	if err != nil {
		t.Fatal(err)
	}
	sig := base64.StdEncoding.EncodeToString(append(fixtureMPI(r), fixtureMPI(s)...))
	// Fixture encodes the block independently; production extraction is not used.
	end := strings.Index(unsigned, "]")
	return []byte(unsigned[:end] + ` SIGN="` + sig + `"` + unsigned[end:])
}

func TestSignedVerifyRealSignatureAndCoverage(t *testing.T) {
	k := signedFixtureKey(t)
	v, err := NewBlockVerifier(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	covered := [][]byte{[]byte(blockHeader + "- first 雪"), []byte(blockHeader + "- second\nline")}
	hashes := make([]string, len(covered))
	for i, r := range covered {
		h := sha256.Sum256(r)
		hashes[i] = base64.StdEncoding.EncodeToString(h[:])
	}
	unsigned := blockHeader + `[ssign VER="0121" RSID="1" SG="0" SPRI="0" GBC="0" FMN="1" CNT="2" HB="` + strings.Join(hashes, " ") + `"] block body`
	raw := fixtureSign(t, k, unsigned)
	if _, err := v.Verify(raw, covered); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		[]byte(strings.Replace(string(raw), "host.example", "evil.example", 1)),
		[]byte(strings.Replace(string(raw), "block body", "block Body", 1)),
		[]byte(strings.Replace(string(raw), `GBC="0"`, `GBC="1"`, 1)),
		append(append([]byte(nil), raw...), ' '),
	} {
		if _, err := v.Verify(bad, covered); err == nil {
			t.Fatal("accepted altered signed bytes")
		}
	}
	for _, bad := range [][][]byte{covered[:1], {covered[1], covered[0]}, {[]byte(blockHeader + "- altered"), covered[1]}} {
		if _, err := v.Verify(raw, bad); err == nil {
			t.Fatal("accepted false coverage")
		}
	}
	// Only the verifier's independent snapshot survives caller key mutation.
	k.Y.SetInt64(1)
	k.G.SetInt64(1)
	if _, err := v.Verify(raw, covered); err != nil {
		t.Fatal("snapshot changed", err)
	}
	if _, err := NewBlockVerifier(&k.PublicKey); err == nil {
		t.Fatal("accepted invalid subgroup values")
	}
	if _, err := (*BlockVerifier)(nil).Verify(raw, covered); err == nil {
		t.Fatal("nil verifier accepted")
	}
}

func TestSignedVerifyCertificateAndMalformedMPI(t *testing.T) {
	k := signedFixtureKey(t)
	v, err := NewBlockVerifier(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := blockHeader + `[ssign-cert VER="0121" RSID="1" SG="0" SPRI="0" TPBL="3" INDEX="1" FLEN="3" FRAG="abc"]`
	raw := fixtureSign(t, k, unsigned)
	if _, err := v.Verify(raw, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(raw, [][]byte{[]byte(blockHeader + "- event")}); err == nil {
		t.Fatal("certificate claimed coverage")
	}
	for _, mpi := range [][]byte{{0, 0}, {0, 9, 0, 1}, {0, 8, 1}, {1, 1, 1}} {
		if _, _, ok := signatureMPI(mpi, 160); ok {
			t.Fatalf("accepted noncanonical MPI %x", mpi)
		}
	}
	if _, err := new(BlockVerifier).Verify(raw, nil); err == nil {
		t.Fatal("uninitialized verifier accepted")
	}
	if _, err := NewBlockVerifier(nil); err == nil {
		t.Fatal("nil key accepted")
	}
}
