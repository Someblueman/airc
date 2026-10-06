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
	if l.DirectoryMailbox == nil || !id(l.DirectoryMailbox.Principal) {
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
	for _, b := range c.Bindings {
		if b.Network == "unix" {
			paths = append(paths, b.Address)
		}
	}
	for _, l := range c.Listeners {
		if l.Protocol == "directory_mailbox" || l.Protocol == "append_log" {
			names := []string{"", "inbox", "processing", "results"}
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
	for i, root := range roots {
		for _, path := range append(paths, roots[i+1:]...) {
			if path == "" {
				continue
			}
			resolved := mailboxResolvedPath(path)
			if mailboxContains(root, resolved) || mailboxContains(resolved, root) {
				return errors.New("directory mailboxes must not overlap state, credentials or other local listeners")
			}
		}
	}
	return nil
}
