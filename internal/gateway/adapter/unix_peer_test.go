//go:build linux || darwin

package adapter

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestListenUnixRejectsForeignUID(t *testing.T) {
	testUnixListenerRejectsForeignUID(t, "unix", ListenUnix)
}

func testUnixListenerRejectsForeignUID(t *testing.T, network string, listen func(string) (net.Listener, error)) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("foreign UID peer test requires root")
	}
	dir, err := os.MkdirTemp("/tmp", "agw-peer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "socket")
	listener, err := listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// Permit only this fixture's foreign child to reach the kernel peer gate.
	if err := os.Chmod(dir, 0711); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	// The ordinary go-build directory can be private to root. Copy the test
	// executable into this disposable traversable fixture for the child.
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	childPath := filepath.Join(dir, "peer-test")
	output, err := os.OpenFile(childPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, source)
	if err := errors.Join(copyErr, output.Close()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(childPath, 0755); err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("peer accept did not join")
		}
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, childPath, "-test.run=^TestUnixForeignUIDChild$")
	child.Env = append(os.Environ(), "AIRC_UNIX_FOREIGN_UID_CHILD=1", "AIRC_UNIX_FOREIGN_UID_PATH="+path, "AIRC_UNIX_FOREIGN_UID_NETWORK="+network)
	child.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1, Gid: 1}}
	if data, err := child.CombinedOutput(); err != nil {
		t.Fatalf("foreign peer: %v\n%s", err, data)
	}
	// An owner connection proves Accept kept serving after rejecting the child.
	owner, err := net.DialTimeout(network, path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	select {
	case conn := <-accepted:
		_ = conn.Close()
	case <-time.After(time.Second):
		t.Fatal("owner was not accepted after foreign rejection")
	}
}

func TestUnixForeignUIDChild(t *testing.T) {
	if os.Getenv("AIRC_UNIX_FOREIGN_UID_CHILD") != "1" {
		return
	}
	conn, err := net.DialTimeout(os.Getenv("AIRC_UNIX_FOREIGN_UID_NETWORK"), os.Getenv("AIRC_UNIX_FOREIGN_UID_PATH"), time.Second)
	if err != nil {
		t.Fatal("foreign child could not reach the peer gate", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if n, err := conn.Read(data[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("foreign peer was not closed: n=%d err=%v", n, err)
	}
}
