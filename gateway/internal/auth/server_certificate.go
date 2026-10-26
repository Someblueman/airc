package auth

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"time"
)

// ServerCertificate snapshots owner-verified bounded files before any listener
// starts. Private bytes and parser errors never enter diagnostics.
func ServerCertificate(certPath, keyPath string) (tls.Certificate, error) {
	certificate, err := openPublic(certPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	defer certificate.Close()
	key, err := openPrivate(keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	defer key.Close()
	certBytes, err := io.ReadAll(io.LimitReader(certificate, (1<<20)+1))
	if err != nil || len(certBytes) > 1<<20 {
		return tls.Certificate{}, ErrCredential
	}
	keyBytes, err := io.ReadAll(io.LimitReader(key, (1<<20)+1))
	defer clear(keyBytes)
	if err != nil || len(keyBytes) > 1<<20 {
		return tls.Certificate{}, ErrCredential
	}
	pair, err := tls.X509KeyPair(certBytes, keyBytes)
	if err != nil || len(pair.Certificate) == 0 {
		return tls.Certificate{}, ErrCredential
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	now := time.Now()
	if err != nil || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return tls.Certificate{}, ErrCredential
	}
	return pair, nil
}
