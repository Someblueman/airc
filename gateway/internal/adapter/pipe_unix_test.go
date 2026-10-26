//go:build darwin || linux

package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPipeStreamRejectsNonPipesAndWrongDirection(t *testing.T) {
	in, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer writer.Close()
	file, err := os.CreateTemp(t.TempDir(), "file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fifoPath := filepath.Join(t.TempDir(), "duplex-fifo")
	if err := unix.Mkfifo(fifoPath, 0600); err != nil {
		t.Fatal(err)
	}
	duplex, err := os.OpenFile(fifoPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer duplex.Close()
	for _, pair := range [][2]*os.File{{file, writer}, {in, file}, {writer, writer}, {in, in}, {nil, writer}, {duplex, writer}, {in, duplex}} {
		if p, err := OpenPipeStream(pair[0], pair[1]); err == nil {
			_ = p.Close()
			t.Fatal("invalid descriptors accepted")
		}
	}
}
