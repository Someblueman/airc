//go:build unix

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"golang.org/x/sys/unix"
)

func TestCredentialCreateReadAndNoOverwrite(t *testing.T) {
	t.Parallel()
	root := privateTemp(t)
	path := filepath.Join(root, "token")
	if err := CreateCredential(path); err != nil {
		t.Fatal(err)
	}
	token, err := ReadToken(path)
	if err != nil || len(token) != 64 {
		t.Fatal("invalid generated credential")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || info.Size() != 65 {
		t.Fatal("incorrect generated file mode or size")
	}
	if err := CreateCredential(path); err == nil {
		t.Fatal("overwrote existing credential")
	}
	after, _ := ReadToken(path)
	if after != token {
		t.Fatal("changed existing credential")
	}
	second := filepath.Join(root, "second")
	if err := CreateCredential(second); err != nil {
		t.Fatal(err)
	}
	other, _ := ReadToken(second)
	if other == token {
		t.Fatal("reused generated credential")
	}
	missing := filepath.Join(root, "missing", "token")
	if err := CreateCredential(missing); err == nil {
		t.Fatal("created unprovisioned parent")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatal("created missing directory")
	}
}

func TestCredentialRejectsUnsafeFilesAndMalformedContent(t *testing.T) {
	t.Parallel()
	root := privateTemp(t)
	path := filepath.Join(root, "token")
	for _, body := range []string{"", strings.Repeat("x", 64), strings.Repeat("a", 63), strings.Repeat("a", 64) + "\n\n", strings.Repeat("a", 1000)} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadToken(path); !errors.Is(err, ErrCredential) {
			t.Fatal("accepted malformed credential")
		}
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); !errors.Is(err, ErrCredential) {
		t.Fatal("accepted group/other-readable credential")
	}
	_ = os.Chmod(path, 0600)
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(link); !errors.Is(err, ErrCredential) {
		t.Fatal("followed credential symlink")
	}
	if err := CreateCredential(link); err == nil {
		t.Fatal("overwrote credential symlink")
	}
	parentLink := filepath.Join(root, "parent-link")
	if err := os.Symlink(root, parentLink); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(filepath.Join(parentLink, "token")); !errors.Is(err, ErrCredential) {
		t.Fatal("followed immediate-parent symlink")
	}
	fifo := filepath.Join(root, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(fifo); !errors.Is(err, ErrCredential) {
		t.Fatal("accepted FIFO")
	}
	if _, err := ReadToken(root); !errors.Is(err, ErrCredential) {
		t.Fatal("accepted directory")
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); !errors.Is(err, ErrCredential) {
		t.Fatal("accepted nonprivate parent directory")
	}
	if err := CreateCredential(filepath.Join(root, "new")); !errors.Is(err, ErrCredential) {
		t.Fatal("created credential in nonprivate directory")
	}
}

func privateTemp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

// fixtureTarget is the room the provider-verification fixtures route to.
var fixtureTarget = core.Target{Kind: "room", Name: "#Allowed"}
