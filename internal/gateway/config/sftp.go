package config

import (
	"errors"
	"net"
	"net/netip"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// SFTP describes one fixed remote mailbox owner using OpenSSH SFTP v3.
type SFTP struct {
	ID             string    `json:"id"`
	Enabled        bool      `json:"enabled"`
	Address        string    `json:"address"`
	Username       string    `json:"username"`
	PrivateKeyFile string    `json:"private_key_file"`
	HostKeyFile    string    `json:"host_key_file"`
	Root           string    `json:"root"`
	RemoteUID      uint32    `json:"remote_uid"`
	Route          SFTPRoute `json:"route"`
}

type SFTPRoute struct {
	Principal              string           `json:"principal"`
	Target                 core.Target      `json:"target"`
	Operations             []core.Operation `json:"operations"`
	RecipientKeyID         string           `json:"recipient_key_id"`
	RecipientPublicKeyFile string           `json:"recipient_public_key_file"`
}

func sftpEndpoint(address string) (string, bool) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || ip.Is4In6() || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.MustParseAddr("255.255.255.255") {
		return "", false
	}
	for i := 0; i < len(portText); i++ {
		if portText[i] < '0' || portText[i] > '9' {
			return "", false
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", false
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(port)), true
}

func sftpUsername(username string) bool {
	if len(username) < 1 || len(username) > 64 || username == "." || username == ".." {
		return false
	}
	for i := 0; i < len(username); i++ {
		b := username[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b == '.') {
			return false
		}
	}
	return true
}

func sftpRoot(root string) bool {
	if len(root) < 2 || len(root) > 512 || !strings.HasPrefix(root, "/") || root == "/" || path.Clean(root) != root {
		return false
	}
	for i := 0; i < len(root); i++ {
		b := root[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '/' || b == '.' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

func sftpOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}

func sftpRootsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (c Config) validateSFTP() error {
	if len(c.SFTP) > 16 {
		return errors.New("SFTP owner count limit exceeded")
	}
	ids := make(map[string]bool, len(c.SFTP))
	type remoteRoot struct{ endpoint, root string }
	var roots []remoteRoot
	for _, owner := range c.SFTP {
		endpoint, validEndpoint := sftpEndpoint(owner.Address)
		if !id(owner.ID) || ids[owner.ID] || !validEndpoint || owner.Address != endpoint || !sftpUsername(owner.Username) || !absolute(owner.PrivateKeyFile) || !absolute(owner.HostKeyFile) || !sftpRoot(owner.Root) || owner.RemoteUID == 0 {
			return errors.New("invalid or duplicate SFTP owner")
		}
		ids[owner.ID] = true
		if owner.Enabled {
			for _, listener := range c.Listeners {
				if listener.Enabled && slices.Contains([]string{"offline_bundle", "file_mailbox", "stdio", "socketpair", "inherited", "fifo", "uart"}, listener.Protocol) {
					return errors.New("SFTP cannot accompany an exclusive command-owned listener")
				}
			}
		}
		for _, prior := range roots {
			if prior.endpoint == endpoint && sftpRootsOverlap(prior.root, owner.Root) {
				return errors.New("SFTP remote roots overlap on the same endpoint")
			}
		}
		roots = append(roots, remoteRoot{endpoint: endpoint, root: owner.Root})

		route := owner.Route
		if !id(route.Principal) || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) || len(route.Operations) == 0 || operations(route.Operations) != nil || c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 {
			return errors.New("SFTP requires a fixed protected route, ingress recipient keys and response signer")
		}
		target := route.Target
		if err := target.Normalize(); err != nil || target != route.Target {
			return errors.New("SFTP route has an invalid target")
		}
		principalFound := false
		for _, principal := range c.Principals {
			if principal.ID != route.Principal || principal.Revoked || len(principal.SigningKeys) == 0 {
				continue
			}
			if !slices.Contains(principal.Targets, target) {
				continue
			}
			principalFound = true
			for _, op := range route.Operations {
				if !sftpOperation(op) || !slices.Contains(principal.Operations, op) {
					return errors.New("SFTP route exceeds the signed principal grant")
				}
				if (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("SFTP history and search routes require a cursor key")
				}
			}
			break
		}
		if !principalFound {
			return errors.New("SFTP route requires an active signing principal with the exact target grant")
		}
	}
	return nil
}
