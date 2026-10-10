// Package testfs provides temporary directories for filesystem and socket tests.
package testfs

import (
	"os"
	"runtime"
)

// MkdirTemp creates a private temporary directory that the caller must remove.
// Darwin needs a short root for Unix socket paths. Other platforms honor TMPDIR,
// allowing Linux journal tests to use a supported filesystem instead of tmpfs.
func MkdirTemp(pattern string) (string, error) {
	root := os.TempDir()
	if runtime.GOOS == "darwin" {
		root = "/tmp"
	}
	return os.MkdirTemp(root, pattern)
}
