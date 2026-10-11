package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func mappedDirectory(path string) bool {
	return len(path) > 1 && len(path) <= 4096 && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\r\n\x00")
}

// These references may designate filesystem objects owned by another carrier.
// Kernel queue names and network addresses do not belong in this comparison.
func (c Config) mappedFileLocalReferences() []string {
	paths := c.sharedMemoryLocalPaths()
	if u := c.Upstream; u != nil && u.Network == "unix" {
		paths = append(paths, u.Address)
	}
	for _, listener := range c.Listeners {
		if listener.Protocol == "shared_memory" && listener.SharedMemory != nil {
			paths = append(paths, listener.SharedMemory.LockDirectory)
		}
	}
	return paths
}

// Resolved spellings alone cannot establish containment on a case-insensitive
// filesystem. Compare existing ancestor identities too; descendants such as
// the state directory may not exist yet when configuration is checked.
func mappedFilePathsOverlap(a, b string) bool {
	a, b = mailboxResolvedPath(a), mailboxResolvedPath(b)
	if mailboxContains(a, b) || mailboxContains(b, a) {
		return true
	}
	contains := func(parent, child string) bool {
		parentInfo, err := os.Stat(parent)
		if err != nil {
			return false
		}
		for {
			if childInfo, err := os.Stat(child); err == nil && os.SameFile(parentInfo, childInfo) {
				return true
			}
			next := filepath.Dir(child)
			if next == child {
				return false
			}
			child = next
		}
	}
	return contains(a, b) || contains(b, a)
}

// validateMappedFile validates a fixed, protected queue endpoint. The queue
// directory and its queue.dat/transaction.lock children are provisioned by the
// operator; this service never creates or resets them.
func (c Config) validateMappedFile() error {
	listeners := map[string]Listener{}
	var directories []string
	for _, listener := range c.Listeners {
		if listener.Protocol != "mapped_file" {
			continue
		}
		if !mappedDirectory(listener.Address) {
			return errors.New("mapped_file requires a bounded canonical absolute queue directory")
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 {
			return errors.New("mapped_file cannot use TLS or origins")
		}
		if listener.Enabled && !Available("mapped_file") {
			return errors.New("mapped_file requires Darwin ARM64 or Linux ARM64")
		}
		for _, prior := range directories {
			if sharedRootsOverlap(prior, listener.Address) {
				return errors.New("mapped_file queue directories overlap")
			}
		}
		directories = append(directories, listener.Address)
		listeners[listener.ID] = listener
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
	}
	// Compare all mapped roots against paths and resources that can name
	// filesystem objects. POSIX and System V queue names are not paths.
	for _, directory := range directories {
		if sharedRootsOverlap(directory, c.StateDir) {
			return errors.New("mapped_file queue directory conflicts with state directory")
		}
		for _, path := range c.sharedMemoryProtectedPaths() {
			if path != "" && sharedRootsOverlap(directory, path) {
				return errors.New("mapped_file queue directory conflicts with a configured key or credential path")
			}
		}
		for _, path := range c.mappedFileLocalReferences() {
			if path != "" && filepath.IsAbs(path) && sharedRootsOverlap(directory, path) {
				return errors.New("mapped_file queue directory conflicts with a configured local resource")
			}
		}
	}
	return nil
}
