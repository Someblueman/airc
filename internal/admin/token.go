// Package admin handles the shared local operator credential. Possession of
// this file grants moderation privileges; a nickname alone never does.
package admin

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/Someblueman/airc/internal/pathcheck"
	"github.com/Someblueman/airc/internal/tokenfmt"
)

func ValidToken(token string) bool { return tokenfmt.Valid(token) }

func CreateToken(path string) error {
	token, err := tokenfmt.New()
	if err != nil {
		return err
	}
	return tokenfmt.CreateExclusive(path, func(file *os.File) error {
		_, err := io.WriteString(file, token+"\n")
		return err
	})
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
