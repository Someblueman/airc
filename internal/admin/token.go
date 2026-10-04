// Package admin handles the shared local operator credential. Possession of
// this file grants moderation privileges; a nickname alone never does.
package admin

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Someblueman/airc/internal/pathcheck"
	"github.com/Someblueman/airc/internal/tokenfmt"
)

func ValidToken(token string) bool { return tokenfmt.Valid(token) }

func CreateToken(path string) error {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, hex.EncodeToString(random[:])+"\n")
	err = errors.Join(writeErr, file.Close())
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func ReadToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !pathcheck.OwnerOnly(info) {
		return "", errors.New("admin token must be a regular file accessible only to its owner (chmod 600)")
	}
	data, err := io.ReadAll(io.LimitReader(file, 66))
	if err != nil {
		return "", err
	}
	token := strings.TrimSuffix(string(data), "\n")
	if !ValidToken(token) {
		return "", errors.New("invalid admin token file; use airc admin init")
	}
	return token, nil
}
