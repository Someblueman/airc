package service

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testfs"
)

func TestUnixgramOwnerPinsIngressAndMailboxInodes(t *testing.T) {
	if !unixgramOwnerAvailable() {
		t.Skip("unixgram unavailable")
	}
	root, err := testfs.MkdirTemp("agw-svc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	mailboxPath := filepath.Join(root, "mailbox")
	mailbox, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: mailboxPath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer mailbox.Close()
	if err := os.Chmod(mailboxPath, 0600); err != nil {
		t.Fatal(err)
	}
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", signing)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := adapter.NewPacketDatagrams(&dispatch.Dispatcher{}, sealer, "realm", []adapter.PacketReturnRoute{{ID: "route", Principal: "principal", Binding: "binding", RecipientID: "recipient", Destination: &net.UnixAddr{Name: mailboxPath, Net: "unixgram"}, Recipient: &recipient.PublicKey}}, make(chan struct{}, 2), make(chan struct{}, 2))
	if err != nil {
		t.Fatal(err)
	}
	ingressPath := filepath.Join(root, "ingress")
	owner, err := listenUnixgramOwner(ingressPath, driver, map[string]os.FileInfo{mailboxPath: mustLstat(t, mailboxPath)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.conn.WriteTo([]byte("probe"), &net.UnixAddr{Name: mailboxPath, Net: "unixgram"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mailboxPath, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.conn.WriteTo([]byte("rejected"), &net.UnixAddr{Name: mailboxPath, Net: "unixgram"}); err == nil {
		t.Fatal("wrote through mailbox after mode change")
	}
	if err := os.Chmod(mailboxPath, 0600); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Close(); err != nil {
		t.Fatal(err)
	}
	// Retain the old filesystem inode: deleting it allows immediate inode-number
	// reuse on Linux and would not exercise a distinct replacement identity.
	retainedPath := filepath.Join(root, "retained-mailbox")
	if err := os.Rename(mailboxPath, retainedPath); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: mailboxPath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := os.Chmod(mailboxPath, 0600); err != nil {
		t.Fatal(err)
	}
	if os.SameFile(mustLstat(t, retainedPath), mustLstat(t, mailboxPath)) {
		t.Fatal("replacement control did not create a distinct inode")
	}
	if _, err := owner.conn.WriteTo([]byte("replacement"), &net.UnixAddr{Name: mailboxPath, Net: "unixgram"}); err == nil {
		t.Fatal("followed replacement mailbox inode")
	}
	if err := owner.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(ingressPath); !os.IsNotExist(err) {
		t.Fatalf("ingress path after close: %v", err)
	}
	if _, err := os.Lstat(mailboxPath); err != nil {
		t.Fatalf("removed client mailbox: %v", err)
	}
}

func mustLstat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func unixgramOwnerAvailable() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

func TestUnixgramCanonicalPathUsesOnlyDarwinAlias(t *testing.T) {
	for _, path := range []string{"/tmp", "/tmp/mailbox", "/tmp-other/mailbox", "/private/tmp/mailbox"} {
		want := path
		if runtime.GOOS == "darwin" {
			switch path {
			case "/tmp":
				want = "/private/tmp"
			case "/tmp/mailbox":
				want = "/private/tmp/mailbox"
			}
		}
		if got := unixgramCanonicalPath(path); got != want {
			t.Fatalf("canonical %q on %s = %q, want %q", path, runtime.GOOS, got, want)
		}
	}
}

func TestUnixgramPathRejectsSharedParent(t *testing.T) {
	if !unixgramOwnerAvailable() {
		t.Skip("unixgram unavailable")
	}
	root, err := testfs.MkdirTemp("agw-parent-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "ingress")
	if err := validateUnixgramSocketPath(path); err != nil {
		t.Fatalf("private owner parent: %v", err)
	}
	if err := os.Chmod(root, 0750); err != nil {
		t.Fatal(err)
	}
	if err := validateUnixgramSocketPath(path); err == nil {
		t.Fatal("accepted shared parent")
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateUnixgramSocketPath(path); err != nil {
		t.Fatalf("restored private owner parent: %v", err)
	}
}
