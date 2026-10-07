// Package testsyslog supplies independent RFC 5848 test origin material.
package testsyslog

import (
	//lint:ignore SA1019 RFC 5848 test signer, never production credential generation.
	"crypto/dsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"math/big"
	"strings"
	"testing"
	"time"
)

type Origin struct {
	Key *dsa.PrivateKey
	DER []byte
}

func New(t testing.TB, before, after time.Time) Origin {
	t.Helper()
	k := new(dsa.PrivateKey)
	if err := dsa.GenerateParameters(&k.Parameters, rand.Reader, dsa.L2048N256); err != nil {
		t.Fatal(err)
	}
	if err := dsa.GenerateKey(k, rand.Reader); err != nil {
		t.Fatal(err)
	}
	return WithKey(t, k, before, after)
}

// WithKey renews public certificate material while retaining the test signing key.
func WithKey(t testing.TB, k *dsa.PrivateKey, before, after time.Time) Origin {
	t.Helper()
	marshal := func(v any) []byte {
		b, err := asn1.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	params := marshal(struct{ P, Q, G *big.Int }{k.P, k.Q, k.G})
	algorithm := pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 1}, Parameters: asn1.RawValue{FullBytes: params}}
	y := marshal(k.Y)
	spki := struct {
		Algorithm pkix.AlgorithmIdentifier
		Key       asn1.BitString
	}{algorithm, asn1.BitString{Bytes: y, BitLength: len(y) * 8}}
	// This fixture is an end-entity pin, not a CA-path fixture. An issuer signature
	// is deliberately unnecessary: signed blocks prove the pinned DSA key.
	sigAlgorithm := pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 3, 101, 112}}
	name := pkix.Name{CommonName: "signed.example"}.ToRDNSequence()
	tbs := marshal(struct {
		Version   int `asn1:"explicit,tag:0"`
		Serial    *big.Int
		Signature pkix.AlgorithmIdentifier
		Issuer    pkix.RDNSequence
		Validity  struct{ Before, After time.Time }
		Subject   pkix.RDNSequence
		PublicKey any
	}{2, big.NewInt(1), sigAlgorithm, name, struct{ Before, After time.Time }{before, after}, name, spki})
	der := marshal(struct {
		TBS       asn1.RawValue
		Algorithm pkix.AlgorithmIdentifier
		Signature asn1.BitString
	}{asn1.RawValue{FullBytes: tbs}, sigAlgorithm, asn1.BitString{Bytes: make([]byte, 64), BitLength: 512}})
	return Origin{k, der}
}

func (o Origin) Sign(t testing.TB, unsigned string) []byte {
	t.Helper()
	h := sha256.Sum256([]byte(unsigned))
	r, s, err := dsa.Sign(rand.Reader, o.Key, h[:])
	if err != nil {
		t.Fatal(err)
	}
	mpi := func(x *big.Int) []byte {
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, uint16(x.BitLen()))
		return append(b, x.Bytes()...)
	}
	signature := base64.StdEncoding.EncodeToString(append(mpi(r), mpi(s)...))
	// Tests supply a single signature/certificate SD element.
	end := strings.IndexByte(unsigned, ']')
	if end < 0 {
		t.Fatal("missing SD element")
	}
	return []byte(unsigned[:end] + ` SIGN="` + signature + `"` + unsigned[end:])
}
