package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestMappedFileReferencesCannotUseQueueOwnedPathExemptions(t *testing.T) {
	for _, kind := range []string{"shared lock root", "unix binding", "unix listener", "unixpacket listener", "uart device", "file response"} {
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
			case "unix binding":
				c.Bindings[0].Network, c.Bindings[0].Address = "unix", file
			case "unix listener":
				c.Listeners = append(c.Listeners, config.Listener{ID: "other", Protocol: "unix", Address: file})
			case "unixpacket listener":
				c.Listeners = append(c.Listeners, config.Listener{ID: "other", Protocol: "unixpacket", Address: file})
			case "uart device":
				c.Listeners = append(c.Listeners, config.Listener{ID: "other", Protocol: "uart", Address: file})
			case "file response":
				c.ReturnRoutes = append(c.ReturnRoutes, config.ReturnRoute{ID: "other", Protocol: "file_mailbox", Destination: file})
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
				c.ReturnRoutes[0].Destination = filepath.Join(alias, "nested")
				provisionMappedCLIDirectory(t, c.ReturnRoutes[0].Destination)
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
			route := c.ReturnRoutes[0]
			route.ID, route.Protocol, route.Destination = "external", "file_mailbox", alias
			route.Listener, route.RecipientKeyID, route.RecipientPublicKeyFile = "", "", ""
			c.ReturnRoutes = append(c.ReturnRoutes, route)
			if err := c.Validate(); err != nil {
				t.Fatal("unexpected non-alias rejection", err)
			}
			if err := c.CheckFiles(); err == nil {
				t.Fatal("configured local output hardlink to queue resource accepted")
			}
		})
	}
}
