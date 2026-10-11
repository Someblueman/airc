package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestMappedFileReferencesCannotUseQueueOwnedPathExemptions(t *testing.T) {
	for _, kind := range []string{"shared lock root", "unix upstream", "unix listener", "unixpacket listener", "uart device"} {
		t.Run(kind, func(t *testing.T) {
			c, root := mappedCLIConfig(t, false)
			if err := c.CheckFiles(); err != nil {
				t.Fatal("valid fixture", err)
			}
			alias := filepath.Join(root, "alias")
			if err := os.Symlink(c.Listeners[0].Address, alias); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(alias, "queue.dat")
			switch kind {
			case "shared lock root":
				c.Listeners = append(c.Listeners, config.Listener{ID: "other", Protocol: "shared_memory", Address: "/other", SharedMemory: &config.SharedMemory{LockDirectory: alias}})
				if err := c.Validate(); err != nil {
					t.Fatal("unexpected lexical rejection", err)
				}
			case "unix upstream":
				c.Upstream.Network, c.Upstream.Address = "unix", file
			case "unix listener":
				c.Listeners = append(c.Listeners, config.Listener{ID: "other", Protocol: "unix", Address: file})
			case "unixpacket listener":
				c.Listeners = append(c.Listeners, config.Listener{ID: "other", Protocol: "unixpacket", Address: file})
			case "uart device":
				c.Listeners = append(c.Listeners, config.Listener{ID: "other", Protocol: "uart", Address: file})
			}
			if err := c.CheckFiles(); err == nil {
				t.Fatal("another connector reference was mistaken for a queue-owned path")
			}
		})
	}
}

func TestMappedFileReferencesRejectCaseAliasedAncestors(t *testing.T) {
	for _, kind := range []string{"state", "credential", "nested response", "external parent"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := mappedCLIConfig(t, false)
			ingress := c.Listeners[0].Address
			alias := filepath.Join(filepath.Dir(ingress), strings.ToUpper(filepath.Base(ingress)))
			actual, err := os.Stat(ingress)
			if err != nil {
				t.Fatal(err)
			}
			other, err := os.Stat(alias)
			if err != nil || !os.SameFile(actual, other) {
				t.Skip("fixture filesystem is case-sensitive")
			}
			switch kind {
			case "state":
				c.StateDir = filepath.Join(alias, "nested-state")
			case "credential":
				c.CursorKeyFile = filepath.Join(alias, "cursor.key")
				if err := os.WriteFile(c.CursorKeyFile, make([]byte, 32), 0600); err != nil {
					t.Fatal(err)
				}
			case "nested response":
				c.Listeners[0].ReplyTo = filepath.Join(alias, "nested")
				provisionMappedCLIDirectory(t, c.Listeners[0].ReplyTo)
			case "external parent":
				parent := filepath.Dir(ingress)
				parentAlias := filepath.Join(filepath.Dir(parent), strings.ToUpper(filepath.Base(parent)))
				c.Listeners = append(c.Listeners, config.Listener{ID: "other", Protocol: "shared_memory", Address: "/other", SharedMemory: &config.SharedMemory{LockDirectory: parentAlias}})
			}
			if err := c.Validate(); err != nil {
				t.Fatal("unexpected lexical rejection", err)
			}
			if err := c.CheckFiles(); err == nil {
				t.Fatal("case-aliased path overlap accepted")
			}
		})
	}
}

func TestMappedFileReferencesRejectExternalHardlinks(t *testing.T) {
	for _, name := range []string{"queue.dat", "transaction.lock"} {
		t.Run(name, func(t *testing.T) {
			c, root := mappedCLIConfig(t, false)
			alias := filepath.Join(root, "local-output")
			if err := os.Link(filepath.Join(c.Listeners[0].Address, name), alias); err != nil {
				t.Fatal(err)
			}
			c.CursorKeyFile = alias
			if err := c.Validate(); err != nil {
				t.Fatal("unexpected non-alias rejection", err)
			}
			if err := c.CheckFiles(); err == nil {
				t.Fatal("configured file hardlinked to a queue resource accepted")
			}
		})
	}
}
