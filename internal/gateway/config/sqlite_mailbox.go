package config

import (
	"errors"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// SQLiteMailbox describes one pre-provisioned local object mailbox database.
type SQLiteMailbox struct {
	ID        string             `json:"id"`
	Enabled   bool               `json:"enabled"`
	Directory string             `json:"directory"`
	Route     SQLiteMailboxRoute `json:"route"`
}

type SQLiteMailboxRoute struct {
	Principal              string           `json:"principal"`
	Target                 core.Target      `json:"target"`
	Operations             []core.Operation `json:"operations"`
	RecipientKeyID         string           `json:"recipient_key_id"`
	RecipientPublicKeyFile string           `json:"recipient_public_key_file"`
}

func SQLiteMailboxAvailable() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

func (c Config) validateSQLiteMailboxes() error {
	if len(c.SQLiteMailboxes) > 16 {
		return errors.New("SQLite mailbox owner count limit exceeded")
	}
	ids := make(map[string]bool, len(c.SQLiteMailboxes))
	var directories []string
	for _, owner := range c.SQLiteMailboxes {
		if !id(owner.ID) || ids[owner.ID] || !mappedDirectory(owner.Directory) || owner.Directory == string(filepath.Separator) {
			return errors.New("invalid or duplicate SQLite mailbox owner")
		}
		ids[owner.ID] = true
		if owner.Enabled {
			if !SQLiteMailboxAvailable() {
				return errors.New("SQLite mailbox requires Darwin or Linux")
			}
			for _, listener := range c.Listeners {
				if listener.Enabled && slices.Contains([]string{"offline_bundle", "file_mailbox", "stdio", "socketpair", "inherited", "fifo", "uart"}, listener.Protocol) {
					return errors.New("SQLite mailbox cannot accompany an exclusive command-owned listener")
				}
			}
		}
		for _, prior := range directories {
			if mappedFilePathsOverlap(prior, owner.Directory) {
				return errors.New("SQLite mailbox directories overlap")
			}
		}
		directories = append(directories, owner.Directory)

		route := owner.Route
		if !id(route.Principal) || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) || len(route.Operations) == 0 || operations(route.Operations) != nil || c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 {
			return errors.New("SQLite mailbox requires a fixed route, response signer and ingress recipient keys")
		}
		target := route.Target
		if err := target.Normalize(); err != nil || target != route.Target {
			return errors.New("SQLite mailbox route has an invalid target")
		}
		principalFound := false
		for _, principal := range c.Principals {
			if principal.ID != route.Principal || principal.Revoked || len(principal.SigningKeys) == 0 {
				continue
			}
			grantedTarget := false
			for _, granted := range principal.Targets {
				if granted.Normalize() == nil && granted == target {
					grantedTarget = true
					break
				}
			}
			if !grantedTarget {
				continue
			}
			principalFound = true
			for _, op := range route.Operations {
				if !sqliteMailboxOperation(op) || !slices.Contains(principal.Operations, op) {
					return errors.New("SQLite mailbox route exceeds the signed principal grant")
				}
				if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("SQLite mailbox history and search routes require a cursor key")
				}
			}
			break
		}
		if !principalFound {
			return errors.New("SQLite mailbox route requires an active signing principal with the exact target grant")
		}
	}

	protectedPaths := c.sharedMemoryProtectedBasePaths()
	for _, owner := range c.SQLiteMailboxes {
		protectedPaths = append(protectedPaths, owner.Route.RecipientPublicKeyFile)
	}
	for _, directory := range directories {
		if mappedFilePathsOverlap(directory, c.StateDir) {
			return errors.New("SQLite mailbox directory overlaps the state directory")
		}
		for _, protected := range protectedPaths {
			if protected == "" {
				continue
			}
			if mappedFilePathsOverlap(directory, protected) {
				return errors.New("SQLite mailbox directory overlaps a configured credential or key")
			}
		}
		for _, local := range c.mappedFileLocalReferences() {
			if local != "" && mappedFilePathsOverlap(directory, local) {
				return errors.New("SQLite mailbox directory overlaps another local carrier")
			}
		}
		for _, listener := range c.Listeners {
			if listener.Protocol == "mapped_file" && mappedFilePathsOverlap(directory, listener.Address) {
				return errors.New("SQLite mailbox directory overlaps a mapped_file queue")
			}
		}
		for _, route := range c.ReturnRoutes {
			if route.Protocol == "mapped_file" && mappedFilePathsOverlap(directory, route.Destination) {
				return errors.New("SQLite mailbox directory overlaps a mapped_file response queue")
			}
		}
	}
	return nil
}

func sqliteMailboxOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}
