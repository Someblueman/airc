//go:build unix

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/testtls"
)

func TestTrustRootsStrictBoundedNativeFile(t *testing.T) {
	cert := testtls.New(t)
	if _, err := ReadTrustRoots(cert.CAPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cert.CAPath)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "roots.pem")
	for _, invalid := range [][]byte{nil, []byte("not PEM"), append([]byte("junk\n"), data...), append(append([]byte{}, data...), []byte("junk")...), []byte(strings.Repeat("x", (1<<20)+1))} {
		if err := os.WriteFile(file, invalid, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTrustRoots(file); !errors.Is(err, ErrCredential) {
			t.Fatal("invalid roots accepted", err)
		}
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrustRoots(file); !errors.Is(err, ErrCredential) {
		t.Fatal("writable trust accepted", err)
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	link := file + ".link"
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrustRoots(link); !errors.Is(err, ErrCredential) {
		t.Fatal("symlink roots accepted", err)
	}
}
