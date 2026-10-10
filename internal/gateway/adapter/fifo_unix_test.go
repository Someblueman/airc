//go:build darwin || linux

package adapter

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func fifoNodes(t *testing.T) string {
	t.Helper()
	root := mailboxDirectory(t)
	for _, name := range []string{"input.fifo", "output.fifo"} {
		if err := unix.Mkfifo(filepath.Join(root, name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fifoFixture(t *testing.T) (*FIFOStream, string, *os.File) {
	t.Helper()
	root := fifoNodes(t)
	reader, err := os.OpenFile(filepath.Join(root, "output.fifo"), os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	pollable, err := duplicatePipe(reader, false)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader = pollable
	t.Cleanup(func() { _ = reader.Close() })
	f, err := OpenFIFOStream(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f, root, reader
}

func fifoWriter(t *testing.T, root string) *os.File {
	t.Helper()
	writer, err := os.OpenFile(filepath.Join(root, "input.fifo"), os.O_WRONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	return writer
}

func TestFIFOFirstContactThenPeerEOF(t *testing.T) {
	f, root, reader := fifoFixture(t)
	if other, err := OpenFIFOStream(root); err == nil {
		_ = other.Close()
		t.Fatal("second root owner admitted")
	}
	result := make(chan error, 1)
	go func() {
		b := make([]byte, 1)
		n, err := f.Read(b)
		if n != 1 || b[0] != 'x' {
			err = errors.Join(err, errors.New("first byte differs"))
		}
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("initial absence completed read: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	writer := fifoWriter(t, root)
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first contact did not wake read")
	}
	if _, err := f.Write([]byte("response")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 8)
	if n, err := io.ReadFull(reader, b); err != nil || n != 8 || string(b) != "response" {
		t.Fatalf("return FIFO: %q %v", b, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := f.Read(b); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("established close: %d %v", n, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := OpenFIFOStream(root)
	if err != nil {
		t.Fatal("closed root lock retained", err)
	}
	_ = fresh.Close()
}
