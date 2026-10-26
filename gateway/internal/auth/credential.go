package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CreateCredential creates, syncs, and closes a new 256-bit random secret (a
// cursor key or a listener password).
// The operator must supply an existing owner-only parent directory. Never
// overwrite a credential, create directories, or print the secret.
func CreateCredential(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrCredential
	}
	var material [32]byte
	if _, err := rand.Read(material[:]); err != nil {
		return err
	}
	encoded := make([]byte, 65)
	hex.Encode(encoded, material[:])
	encoded[64] = '\n'
	clear(material[:])
	defer clear(encoded)
	return createPrivate(path, encoded)
}

// ReadToken verifies ownership, permissions and regular-file identity on the
// opened descriptor before a bounded read. No secret content enters errors.
func ReadToken(path string) (string, error) {
	f, err := openPrivate(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil {
		return "", ErrCredential
	}
	defer clear(data)
	token := strings.TrimSuffix(string(data), "\n")
	if len(token) != 64 {
		return "", ErrCredential
	}
	material, err := hex.DecodeString(token)
	clear(material)
	if err != nil {
		return "", ErrCredential
	}
	return token, nil
}

// finishCreation records all write/close errors without deleting an existing
// credential. Only the native exclusive-create owner calls this helper.
func finishCreation(f *os.File, data []byte) error {
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
