//go:build darwin || linux

package gateway_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testfs"
)

// unixStreamFixture starts the real gateway process on an owner-only Unix
// framed listener. The path is private to this test and is never reused.
func unixStreamFixture(t *testing.T) (net.Conn, *detachedCredentials, func(), string, string) {
	t.Helper()
	socket := fixture(t)
	const account = fixtureAccount
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, account)
	credentials := provisionDetached(t, &c)
	c.Principals[0].BearerTokenFile = ""
	c.Principals[0].Operations = dispatch.ImplementedOperations()
	pathRoot, err := testfs.MkdirTemp("us-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(pathRoot) })
	pathRoot, err = filepath.EvalSymlinks(pathRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pathRoot, 0700); err != nil {
		t.Fatal(err)
	}
	listenerPath := filepath.Join(pathRoot, "g.sock")
	c.CursorKeyFile = filepath.Join(pathRoot, "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "unix", Protocol: "unix", Address: listenerPath, Enabled: true, Operations: dispatch.ImplementedOperations()}}
	binary := gatewayBinary(t)
	p := startGateway(t, binary, c)
	address := gatewayAddress(t, p)
	initial, err := os.Lstat(address)
	if err != nil {
		t.Fatal(err)
	}
	// A second real gateway process must reject the active socket path while
	// leaving the original listener usable.
	duplicate := c
	duplicate.StateDir = filepath.Join(pathRoot, "duplicate-state")
	if err := os.Mkdir(duplicate.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	second := startGateway(t, binary, duplicate)
	select {
	case <-second.done:
		if second.err == nil || !strings.Contains(second.stderr.String(), "unix listener path already exists") {
			t.Fatal("duplicate gateway did not reject the active socket path", second.err, second.stderr.String())
		}
	case <-time.After(3 * time.Second):
		_ = second.command.Process.Kill()
		t.Fatal("duplicate gateway did not reject active Unix socket")
	}
	remaining, err := os.Lstat(address)
	if err != nil || !os.SameFile(initial, remaining) {
		t.Fatalf("duplicate startup replaced the active socket: %v", err)
	}
	conn, err := net.DialTimeout("unix", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	stop := func() {
		if err := p.command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatal(p.err, p.stderr.String())
			}
		case <-time.After(8 * time.Second):
			t.Fatal("unix gateway did not join")
		}
		if _, err := os.Lstat(address); !os.IsNotExist(err) {
			t.Fatalf("unix socket survived shutdown: %v", err)
		}
	}
	return conn, credentials, stop, address, socket
}
