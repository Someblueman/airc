package auth

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"slices"
)

// ReadPostgresClientCertificate uses the bounded private-file key-pair loader,
// then requires this profile's Ed25519 client identity and exact database role.
// The caller owns the returned private key and clears it after all TLS users join.
func ReadPostgresClientCertificate(certPath, keyPath, username string) (tls.Certificate, error) {
	pair, err := ServerCertificate(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, ErrCredential
	}
	key, ok := pair.PrivateKey.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return tls.Certificate{}, ErrCredential
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.IsCA || leaf.Subject.CommonName != username || !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth) || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		clear(key)
		return tls.Certificate{}, ErrCredential
	}
	pair.Leaf = leaf
	return pair, nil
}
