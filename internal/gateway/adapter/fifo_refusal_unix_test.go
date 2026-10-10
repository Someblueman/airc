//go:build darwin || linux

package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFIFORefusesUnsafeRootsAndNodes(t *testing.T) {
	for _, kind := range []string{"relative", "root-mode", "root-symlink", "input-file", "output-file", "input-symlink", "node-mode", "alias"} {
		t.Run(kind, func(t *testing.T) {
			root := fifoNodes(t)
			in, out := filepath.Join(root, "input.fifo"), filepath.Join(root, "output.fifo")
			path := root
			switch kind {
			case "relative":
				path = "relative"
			case "root-mode":
				if err := os.Chmod(root, 0755); err != nil {
					t.Fatal(err)
				}
			case "root-symlink":
				path = filepath.Join(mailboxDirectory(t), "link")
				if err := os.Symlink(root, path); err != nil {
					t.Fatal(err)
				}
			case "input-file", "output-file":
				name := in
				if kind == "output-file" {
					name = out
				}
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			case "input-symlink":
				if err := os.Remove(in); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(out, in); err != nil {
					t.Fatal(err)
				}
			case "node-mode":
				if err := os.Chmod(in, 0644); err != nil {
					t.Fatal(err)
				}
			case "alias":
				if err := os.Remove(out); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(in, out); err != nil {
					t.Fatal(err)
				}
			}
			if f, err := OpenFIFOStream(path); err == nil {
				_ = f.Close()
				t.Fatal("unsafe FIFO accepted")
			}
		})
	}
}

func TestFIFOAbsentReturnReaderReleasesRoot(t *testing.T) {
	root := fifoNodes(t)
	if f, err := OpenFIFOStream(root); err == nil {
		_ = f.Close()
		t.Fatal("missing return reader accepted")
	}
	reader, err := os.OpenFile(filepath.Join(root, "output.fifo"), os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	f, err := OpenFIFOStream(root)
	if err != nil {
		t.Fatal("failed open retained lock or descriptors", err)
	}
	defer f.Close()
	for _, file := range []*os.File{f.pipes.input, f.pipes.output} {
		raw, err := file.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var flags int
		var inspectErr error
		if err := raw.Control(func(fd uintptr) { flags, inspectErr = unix.FcntlInt(fd, unix.F_GETFD, 0) }); err != nil {
			t.Fatal(err)
		}
		if inspectErr != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("FIFO duplicate not close-on-exec: %d %v", flags, inspectErr)
		}
	}
}
