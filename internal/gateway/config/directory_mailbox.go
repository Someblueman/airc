package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Resolve existing ancestors too: state directories need not exist yet.
func mailboxResolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(mailboxResolvedPath(parent), filepath.Base(path))
}

func mailboxContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (c Config) checkMailboxReferences(paths []string) error {
	var roots []string
	if u := c.Upstream; u != nil && u.Network == "unix" {
		paths = append(paths, u.Address)
	}
	for _, l := range c.Listeners {
		if t, _ := Lookup(l.Protocol); t.directories != nil {
			for _, name := range t.directories {
				info, err := os.Lstat(filepath.Join(l.Address, name))
				if err != nil {
					return err
				}
				if !info.IsDir() || info.Mode().Perm() != 0700 {
					return errors.New("directory mailbox requires existing private directories")
				}
			}
			roots = append(roots, mailboxResolvedPath(l.Address))
		}
		if l.Protocol == "unix" || l.Protocol == "unixgram" {
			paths = append(paths, l.Address)
		}
	}
	for i, root := range roots {
		for _, path := range paths {
			if path == "" {
				continue
			}
			resolved := mailboxResolvedPath(path)
			if mailboxContains(root, resolved) || mailboxContains(resolved, root) {
				return errors.New("queue directories must not overlap state, credentials or other local resources")
			}
		}
		for _, other := range roots[i+1:] {
			if mailboxContains(root, other) || mailboxContains(other, root) {
				return errors.New("queue directories must not overlap state, credentials or other local resources")
			}
		}
	}
	return nil
}
