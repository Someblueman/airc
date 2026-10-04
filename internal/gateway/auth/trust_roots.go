package auth

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"io"
)

// ReadTrustRoots uses the same native ownership and no-follow checks as public
// verification keys. Custom roots must be owner-provisioned and not writable by
// others. An empty path is handled by the caller using system trust instead.
func ReadTrustRoots(path string) (*x509.CertPool, error) {
	f, err := openPublic(path)
	if err != nil {
		return nil, ErrCredential
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, ErrCredential
	}
	pool := x509.NewCertPool()
	count := 0
	for len(bytes.TrimSpace(data)) != 0 {
		data = bytes.TrimSpace(data)
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, ErrCredential
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, ErrCredential
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, ErrCredential
		}
		pool.AddCert(cert)
		count++
		data = rest
	}
	if count == 0 {
		return nil, ErrCredential
	}
	return pool, nil
}
