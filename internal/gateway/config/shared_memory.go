package config

import (
	"errors"
	"path/filepath"
	"strings"
)

// SharedMemory selects one pre-provisioned directory root for shared-memory
// ingress and every protected response queue.
type SharedMemory struct {
	LockDirectory string `json:"lock_directory"`
}

func sharedMemoryName(name string) bool {
	if len(name) < 2 || len(name) > 31 || name[0] != '/' {
		return false
	}
	const first = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const rest = first + "_.-"
	if !strings.ContainsRune(first, rune(name[1])) {
		return false
	}
	for i := 2; i < len(name); i++ {
		if !strings.ContainsRune(rest, rune(name[i])) {
			return false
		}
	}
	return true
}

func sharedRootsOverlap(a, b string) bool {
	within := func(root, path string) bool {
		if root == string(filepath.Separator) {
			return filepath.IsAbs(path)
		}
		return strings.HasPrefix(path, root+string(filepath.Separator))
	}
	return a == b || within(a, b) || within(b, a)
}

// validateSharedMemory validates names, option placement, protection, route
// privacy and path relationships without opening or creating any resource.
func (c Config) validateSharedMemory() error {
	listeners := make(map[string]Listener)
	var roots []string
	ingress := make(map[string]string)
	for _, listener := range c.Listeners {
		if listener.Protocol != "shared_memory" {
			if listener.SharedMemory != nil {
				return errors.New("shared_memory options require shared_memory protocol")
			}
			continue
		}
		if listener.SharedMemory == nil || !sharedMemoryName(listener.Address) || !filepath.IsAbs(listener.SharedMemory.LockDirectory) || filepath.Clean(listener.SharedMemory.LockDirectory) != listener.SharedMemory.LockDirectory || len(listener.SharedMemory.LockDirectory) > 4096 || strings.ContainsAny(listener.SharedMemory.LockDirectory, "\r\n\x00") {
			return errors.New("shared_memory requires a /name and canonical absolute lock directory")
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 {
			return errors.New("shared_memory cannot use TLS or origins")
		}
		root := listener.SharedMemory.LockDirectory
		if sharedRootsOverlap(root, c.StateDir) {
			return errors.New("shared_memory lock directory conflicts with state directory")
		}
		for _, path := range c.sharedMemoryProtectedPaths() {
			if path != "" && sharedRootsOverlap(root, path) {
				return errors.New("shared_memory lock directory conflicts with a configured key or credential path")
			}
		}
		for _, path := range c.sharedMemoryLocalPaths() {
			if path != "" && filepath.IsAbs(path) && sharedRootsOverlap(root, path) {
				return errors.New("shared_memory lock directory conflicts with a configured local resource")
			}
		}
		for _, other := range roots {
			if root != other && sharedRootsOverlap(root, other) {
				return errors.New("shared_memory lock directories overlap")
			}
		}
		roots = append(roots, root)
		listeners[listener.ID] = listener
		if priorRoot, exists := ingress[listener.Address]; exists {
			if priorRoot == root {
				return errors.New("duplicate shared_memory ingress name in lock directory")
			}
			return errors.New("shared_memory ingress name is global across lock directories")
		}
		ingress[listener.Address] = root
		if listener.Enabled && !Available("shared_memory") {
			return errors.New("shared_memory requires Darwin ARM64 or Linux ARM64")
		}
	}

	// A reply queue must not be any listener's ingress: replies would loop.
	for _, listener := range listeners {
		if listener.ReplyTo != "" && hasIngressName(ingress, listener.ReplyTo) {
			return errors.New("shared_memory reply_to aliases an ingress name")
		}
	}
	return nil
}

func hasIngressName(ingress map[string]string, name string) bool {
	_, exists := ingress[name]
	return exists
}

func (c Config) sharedMemoryProtectedPaths() []string {
	paths := []string{c.CursorKeyFile}
	for _, owner := range c.SFTP {
		paths = append(paths, owner.PrivateKeyFile, owner.HostKeyFile)
	}
	paths = append(paths, c.boundAuthorFiles()...)
	for _, owner := range c.PostgresNotify {
		paths = append(paths, owner.ClientCertFile, owner.ClientKeyFile, owner.TLSCAFile)
	}
	if u := c.Upstream; u != nil {
		paths = append(paths, u.AccessTokenFile, u.GatewayTokenFile, u.TLSCAFile)
	}
	for _, listener := range c.Listeners {
		paths = append(paths, listener.TLSCertFile, listener.TLSKeyFile, listener.PasswordFile)
	}
	return paths
}

func (c Config) sharedMemoryLocalPaths() []string {
	paths := make([]string, 0, len(c.Listeners))
	for _, listener := range c.Listeners {
		// Mapped-file queue directories are checked against these paths.
		if t, _ := Lookup(listener.Protocol); t.Address == Path && t.Name != "mapped_file" {
			paths = append(paths, listener.Address)
		}
	}
	return paths
}
