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
	mappedRoots := map[string]bool{}
	if u := c.Upstream; u != nil && u.Network == "unix" {
		paths = append(paths, u.Address)
	}
	for _, l := range c.Listeners {
		if l.Protocol == "shared_memory" && l.SharedMemory != nil {
			paths = append(paths, l.SharedMemory.LockDirectory)
		}
		if l.Protocol == "mapped_file" {
			// The ingress queue and its provisioned reply queue alike.
			for _, queue := range []string{l.Address, l.ReplyTo} {
				if queue == "" {
					continue
				}
				info, err := os.Lstat(queue)
				if err != nil {
					return err
				}
				if !info.IsDir() || info.Mode().Perm() != 0700 {
					return errors.New("mapped_file queue must be a private directory")
				}
				for _, name := range []string{"queue.dat", "transaction.lock"} {
					path := filepath.Join(queue, name)
					child, err := os.Lstat(path)
					if err != nil {
						return err
					}
					if !child.Mode().IsRegular() || child.Mode().Perm() != 0600 || name == "queue.dat" && child.Size() != 16<<10 {
						return errors.New("mapped_file queue resources must be regular owner-only files")
					}
				}
				roots = append(roots, mailboxResolvedPath(queue))
				mappedRoots[mailboxResolvedPath(queue)] = true
			}
		}
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
	// Include every local carrier reference for mapped queues, including
	// response paths and devices that are not directory-mailbox roots.
	for root := range mappedRoots {
		for _, path := range c.mappedFileLocalReferences() {
			if path == "" {
				continue
			}
			if mappedFilePathsOverlap(root, path) {
				return errors.New("mapped_file directories must not overlap other local carrier paths")
			}
		}
	}
	for i, root := range roots {
		for _, path := range paths {
			if path == "" {
				continue
			}
			resolved := mailboxResolvedPath(path)
			if mailboxContains(root, resolved) || mailboxContains(resolved, root) || mappedRoots[root] && mappedFilePathsOverlap(root, resolved) {
				return errors.New("queue directories must not overlap state, credentials or other local resources")
			}
		}
		for _, other := range roots[i+1:] {
			if mailboxContains(root, other) || mailboxContains(other, root) || (mappedRoots[root] || mappedRoots[other]) && mappedFilePathsOverlap(root, other) {
				return errors.New("queue directories must not overlap state, credentials or other local resources")
			}
		}
	}
	return nil
}
