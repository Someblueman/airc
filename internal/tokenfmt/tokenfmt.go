// Package tokenfmt defines the shape of airc bearer tokens (32 random bytes,
// hex encoded) so clients and servers validate them identically.
package tokenfmt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// New returns a fresh token: 32 random bytes, hex encoded.
func New() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

// CreateExclusive creates path (and its directory) as an owner-only file that
// must not already exist, lets write fill it, and removes it again if writing
// or closing fails, so a credential file is either complete or absent.
func CreateExclusive(path string, write func(*os.File) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := errors.Join(write(file), file.Close()); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// Valid reports whether token is 64 hex characters.
func Valid(token string) bool {
	if len(token) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(token)
	return err == nil && len(decoded) == 32
}
