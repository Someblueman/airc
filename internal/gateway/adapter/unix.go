package adapter

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrUnixUnsupported = errors.New("unix listeners are unsupported on this platform")

// UnixListener is an owner-only local framed listener. Its socket path is
// removed on close only when it is still the exact inode created here.
type UnixListener struct {
	net.Listener
	path string
	file os.FileInfo
	once sync.Once
	err  error
}

func ListenUnix(path string) (net.Listener, error) {
	if !unixPlatformSupported() {
		return nil, ErrUnixUnsupported
	}
	if err := validateUnixPath(path); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		_ = listener.Close()
		return nil, errors.New("unix listener has unexpected implementation")
	}
	unixListener.SetUnlinkOnClose(false)
	created, statErr := os.Lstat(path)
	if statErr != nil {
		_ = listener.Close()
		return nil, statErr
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		removeSameUnixSocket(path, created)
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || !os.SameFile(info, created) || !unixPathOwner(info) {
		_ = listener.Close()
		removeSameUnixSocket(path, created)
		if err != nil {
			return nil, err
		}
		return nil, errors.New("created unix socket failed ownership check")
	}
	return &UnixListener{Listener: listener, path: path, file: info}, nil
}

func removeSameUnixSocket(path string, created os.FileInfo) {
	info, err := os.Lstat(path)
	if err == nil && os.SameFile(info, created) {
		_ = os.Remove(path)
	}
}

func (l *UnixListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := unixPeerAllowed(conn); err == nil {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func (l *UnixListener) Close() error {
	l.once.Do(func() {
		l.err = l.Listener.Close()
		info, err := os.Lstat(l.path)
		if err == nil && os.SameFile(info, l.file) {
			if removeErr := os.Remove(l.path); l.err == nil {
				l.err = removeErr
			}
		}
	})
	return l.err
}

func validateUnixPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
		return errors.New("unix listener path must be absolute and normalized")
	}
	parent := filepath.Dir(path)
	for current := parent; ; {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("unix listener parent: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("unix listener path contains symlink or non-directory")
		}
		if current == parent && (info.Mode().Perm()&0077 != 0 || !unixPathOwner(info)) {
			return errors.New("unix listener parent is not owner-controlled")
		}
		if current != parent && !unixTrustedAncestor(info) {
			return errors.New("unix listener ancestor is writable or untrusted")
		}
		if current == string(filepath.Separator) {
			break
		}
		current = filepath.Dir(current)
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("unix listener path already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
