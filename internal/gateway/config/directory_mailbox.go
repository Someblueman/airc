package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// DirectoryMailbox fixes the authenticated owner of one provisioned directory.
type DirectoryMailbox struct {
	Principal string `json:"principal"`
}

func (c Config) validateDirectoryMailbox(l Listener) error {
	if l.Protocol != "directory_mailbox" {
		if l.DirectoryMailbox != nil {
			return errors.New("directory mailbox options require a directory_mailbox listener")
		}
		return nil
	}
	if c.LocalAuthorMode(l) {
		// Plain author requests: the private directory is the access gate.
		return nil
	}
	if !id(l.DirectoryMailbox.Principal) {
		return errors.New("directory mailbox requires an explicit owner principal")
	}
	for _, p := range c.Principals {
		if p.ID == l.DirectoryMailbox.Principal {
			if p.Revoked || len(p.SigningKeys) == 0 {
				return errors.New("directory mailbox owner must be active with a signing key")
			}
			return nil
		}
	}
	return errors.New("directory mailbox owner is unknown")
}

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
	for _, b := range c.Bindings {
		if b.Network == "unix" {
			paths = append(paths, b.Address)
		}
	}
	for _, l := range c.Listeners {
		if l.Protocol == "shared_memory" && l.SharedMemory != nil {
			paths = append(paths, l.SharedMemory.LockDirectory)
		}
		if l.Protocol == "mapped_file" {
			info, err := os.Lstat(l.Address)
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode().Perm() != 0700 {
				return errors.New("mapped_file queue must be a private directory")
			}
			for _, name := range []string{"queue.dat", "transaction.lock"} {
				path := filepath.Join(l.Address, name)
				child, err := os.Lstat(path)
				if err != nil {
					return err
				}
				if !child.Mode().IsRegular() || child.Mode().Perm() != 0600 || name == "queue.dat" && child.Size() != 16<<10 {
					return errors.New("mapped_file queue resources must be regular owner-only files")
				}
			}
			roots = append(roots, mailboxResolvedPath(l.Address))
			mappedRoots[mailboxResolvedPath(l.Address)] = true
		}
		if l.Protocol == "directory_mailbox" || l.Protocol == "append_log" || l.Protocol == "fifo" {
			names := []string{"", "inbox", "processing", "results"}
			if l.Protocol == "fifo" {
				names = []string{""}
			}
			if l.Protocol == "append_log" {
				names = []string{"", "logs"}
			}
			for _, name := range names {
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
	for _, route := range c.ReturnRoutes {
		if route.Protocol != "mapped_file" {
			continue
		}
		info, err := os.Lstat(route.Destination)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode().Perm() != 0700 {
			return errors.New("mapped_file response queue must be a private directory")
		}
		for _, name := range []string{"queue.dat", "transaction.lock"} {
			path := filepath.Join(route.Destination, name)
			child, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !child.Mode().IsRegular() || child.Mode().Perm() != 0600 || name == "queue.dat" && child.Size() != 16<<10 {
				return errors.New("mapped_file queue resources must be regular owner-only files")
			}
		}
		roots = append(roots, mailboxResolvedPath(route.Destination))
		mappedRoots[mailboxResolvedPath(route.Destination)] = true
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
