package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/Someblueman/airc/internal/gateway/store"
)

type packetOwner struct {
	conn   net.PacketConn
	driver interface {
		Serve(context.Context, net.PacketConn) error
	}
	path string
	file os.FileInfo
	once sync.Once
	err  error
}

func (o *packetOwner) addr() net.Addr { return o.conn.LocalAddr() }
func (o *packetOwner) close() error {
	o.once.Do(func() {
		o.err = o.conn.Close()
		if o.path != "" {
			if info, err := os.Lstat(o.path); err == nil && os.SameFile(info, o.file) {
				if err := os.Remove(o.path); o.err == nil {
					o.err = err
				}
			}
		}
	})
	return o.err
}
func (o *packetOwner) serve(ctx context.Context) error { return o.driver.Serve(ctx, o.conn) }

func listenUnixgramOwner(path string, driver packetDriver) (*packetOwner, error) {
	if err := validateUnixgramSocketPath(path); err != nil {
		return nil, err
	}
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	created, err := os.Lstat(path)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = conn.Close()
		removeUnixgramInode(path, created)
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || !os.SameFile(info, created) || !unixgramOwner(info) {
		_ = conn.Close()
		removeUnixgramInode(path, created)
		if err != nil {
			return nil, err
		}
		return nil, store.ErrInvalid
	}
	return &packetOwner{conn: conn, driver: driver, path: path, file: info}, nil
}

func removeUnixgramInode(path string, created os.FileInfo) {
	info, err := os.Lstat(path)
	if err == nil && os.SameFile(info, created) {
		_ = os.Remove(path)
	}
}

func validateUnixgramSocketPath(path string) error {
	return validateUnixgramPath(path, true)
}

func validateUnixgramPath(path string, absent bool) error {
	limit := 107
	if runtime.GOOS == "darwin" {
		limit = 103
	}
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") || len(path) > limit {
		return store.ErrInvalid
	}
	parent := filepath.Dir(unixgramCanonicalPath(path))
	for current := parent; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return store.ErrInvalid
		}
		if current == parent {
			if !(unixgramOwner(info) && info.Mode().Perm()&0077 == 0) && !(info.Mode()&os.ModeSticky != 0 && unixgramRootOwned(info)) {
				return store.ErrInvalid
			}
		} else if !unixgramTrustedAncestor(info) {
			return store.ErrInvalid
		}
		if current == string(filepath.Separator) {
			break
		}
	}
	if absent {
		if _, err := os.Lstat(path); err == nil {
			return store.ErrInvalid
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func unixgramCanonicalPath(path string) string {
	if runtime.GOOS != "darwin" {
		return path
	}
	if path == "/tmp" {
		return "/private/tmp"
	}
	if strings.HasPrefix(path, "/tmp/") {
		return "/private" + path
	}
	return path
}
