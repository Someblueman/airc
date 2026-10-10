package config

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
)

// SharedMemory selects one pre-provisioned directory root for shared-memory
// ingress and every protected response queue.
type SharedMemory struct {
	LockDirectory string `json:"lock_directory"`
}

// SharedMemoryAvailable is deliberately narrower than the adapter's Unix
// syscall availability: only the exercised ARM64 targets are accepted.
func SharedMemoryAvailable() bool {
	return runtime.GOARCH == "arm64" && (runtime.GOOS == "darwin" || runtime.GOOS == "linux")
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
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("shared_memory cannot use TLS, origins or anonymous access")
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
		if listener.Enabled && !SharedMemoryAvailable() {
			return errors.New("shared_memory requires Darwin ARM64 or Linux ARM64")
		}
	}

	counts := make(map[string]int)
	type responseOwner struct{ root, principal string }
	responseOwners := make(map[string]responseOwner)
	for _, route := range c.ReturnRoutes {
		if route.Protocol != "shared_memory" {
			continue
		}
		listener, ok := listeners[route.Listener]
		if !ok || !sharedMemoryName(route.Destination) || hasIngressName(ingress, route.Destination) || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) || route.Listener == "" {
			return errors.New("invalid protected shared_memory return route")
		}
		root := listener.SharedMemory.LockDirectory
		if owner, exists := responseOwners[route.Destination]; exists && (owner.root != root || owner.principal != route.Principal) {
			return errors.New("shared_memory response name must have one root and principal globally")
		}
		responseOwners[route.Destination] = responseOwner{root: root, principal: route.Principal}
		counts[route.Listener+"\x00"+route.Principal]++
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("enabled shared_memory requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("shared_memory requires exactly one protected return route per principal")
			}
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
	for _, owner := range c.S3 {
		paths = append(paths, owner.CredentialFile, owner.Route.RecipientPublicKeyFile)
	}
	for _, owner := range c.SFTP {
		paths = append(paths, owner.PrivateKeyFile, owner.HostKeyFile, owner.Route.RecipientPublicKeyFile)
	}
	if c.ResponseSigningKey != nil {
		paths = append(paths, c.ResponseSigningKey.PrivateKeyFile)
	}
	for _, binding := range c.Bindings {
		paths = append(paths, binding.AccountTokenFile, binding.AccessTokenFile, binding.TLSCAFile)
	}
	for _, principal := range c.Principals {
		paths = append(paths, principal.BearerTokenFile)
		for _, key := range principal.SigningKeys {
			paths = append(paths, key.PublicKeyFile)
		}
	}
	for _, key := range c.RecipientKeys {
		paths = append(paths, key.PrivateKeyFile)
	}
	for _, listener := range c.Listeners {
		if listener.TwilioSMS != nil {
			paths = append(paths, listener.TwilioSMS.AuthTokenFile)
		}
		if listener.PostmarkEmail != nil {
			paths = append(paths, listener.PostmarkEmail.PasswordFile)
		}
		paths = append(paths, listener.TLSCertFile, listener.TLSKeyFile)
	}
	for _, route := range c.ReturnRoutes {
		paths = append(paths, route.RecipientPublicKeyFile)
	}
	return paths
}

func (c Config) sharedMemoryLocalPaths() []string {
	paths := make([]string, 0, len(c.Listeners)+len(c.ReturnRoutes))
	for _, listener := range c.Listeners {
		switch listener.Protocol {
		case "unix", "unixpacket", "unixgram", "file_mailbox", "directory_mailbox", "append_log", "fifo", "uart":
			paths = append(paths, listener.Address)
		}
	}
	for _, route := range c.ReturnRoutes {
		switch route.Protocol {
		case "unixgram", "file_mailbox", "directory_mailbox", "append_log", "fifo":
			paths = append(paths, route.Destination)
		}
	}
	return paths
}
