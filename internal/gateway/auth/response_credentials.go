package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
)

// ReadResponseSigningKey reads an owner-provisioned PKCS#8 Ed25519 private
// key. The returned key is independent of the file buffer and is suitable for
// NewResponseSealer.
func ReadResponseSigningKey(path string) (ed25519.PrivateKey, error) {
	data, err := keyFile(path, true)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.HasPrefix(data, []byte("-----BEGIN PRIVATE KEY-----")) {
		return nil, ErrCredential
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	clear(block.Bytes)
	key, ok := parsed.(ed25519.PrivateKey)
	if err != nil || !ok || len(key) != ed25519.PrivateKeySize || !validSigningKey(key) {
		return nil, ErrCredential
	}
	result := append(ed25519.PrivateKey(nil), key...)
	clear(key)
	return result, nil
}

// ReadResponseRecipientKey reads an owner-provisioned PKIX P-256 public key
// for ECDH-ES response encryption. Other curves and key types are rejected.
func ReadResponseRecipientKey(path string) (*ecdsa.PublicKey, error) {
	data, err := keyFile(path, false)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.HasPrefix(data, []byte("-----BEGIN PUBLIC KEY-----")) {
		return nil, ErrCredential
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	clear(block.Bytes)
	key, ok := parsed.(*ecdsa.PublicKey)
	if err != nil || !ok || key == nil {
		return nil, ErrCredential
	}
	encoded, encodeErr := key.Bytes()
	if encodeErr != nil || key.Curve != elliptic.P256() || len(encoded) != 65 {
		return nil, ErrCredential
	}
	if _, err := key.ECDH(); err != nil {
		return nil, ErrCredential
	}
	return key, nil
}
