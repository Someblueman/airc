package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Someblueman/airc/internal/pathcheck"
)

// Load uses the same parser as future daemon startup. It does not provision or
// change files, and verifies configured credential permissions before success.
func Load(path string) (Config, error) {
	c, err := ParseFile(path)
	if err != nil {
		return c, err
	}
	return c, c.CheckFiles()
}

// ParseFile reads and validates the configuration file like Load, without
// checking the files it references: for a command that is about to create one
// of them.
func ParseFile(path string) (Config, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return Config{}, err
	}
	if !before.Mode().IsRegular() {
		return Config{}, errors.New("configuration must be a regular file, not a symlink or pipe")
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Config{}, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return Config{}, errors.New("configuration must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// CheckFiles validates references without returning or printing secret content.
// Actual token/certificate decoding belongs to authentication/backend startup.
func (c Config) CheckFiles() error {
	paths := []string{c.StateDir}
	var mappedPaths []string
	check := func(path string, private bool) error {
		if path == "" {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("credential reference unavailable: %w", err)
		}
		if !info.Mode().IsRegular() || private && info.Mode().Perm()&0077 != 0 {
			return errors.New("credential reference must be a regular file with owner-only private permissions")
		}
		paths = append(paths, path)
		return nil
	}
	// Some references may repeat within one role: listeners sharing a
	// certificate, key or server password, which may also be the upstream's
	// access token (the service's one connection credential) or trust root.
	// A path in two roles, or aliasing anything else, still fails below.
	shared := map[string]string{}
	share := func(path string, private bool, role string) error {
		if path != "" && shared[path] == role {
			return nil
		}
		if path != "" {
			shared[path] = role
		}
		return check(path, private)
	}
	if err := check(c.CursorKeyFile, true); err != nil {
		return err
	}
	for _, b := range c.RedisPubSub {
		for _, path := range []string{b.Subscriber.PasswordFile, b.Publisher.PasswordFile} {
			if err := check(path, true); err != nil {
				return err
			}
		}
		if err := check(b.TLSCAFile, false); err != nil {
			return err
		}
	}
	for _, b := range c.RedisStreams {
		if err := check(b.Credential.PasswordFile, true); err != nil {
			return err
		}
		if err := check(b.TLSCAFile, false); err != nil {
			return err
		}
	}
	for _, owner := range c.SFTP {
		if err := check(owner.PrivateKeyFile, true); err != nil {
			return err
		}
		if err := check(owner.HostKeyFile, false); err != nil {
			return err
		}
	}
	// One AIRC account may be mapped on several bridges with one token file.
	accountFiles := map[string]bool{}
	checkAccounts := func(accounts []BridgeAccount) error {
		for _, a := range accounts {
			if accountFiles[a.AccountTokenFile] {
				continue
			}
			accountFiles[a.AccountTokenFile] = true
			if err := check(a.AccountTokenFile, true); err != nil {
				return err
			}
		}
		return nil
	}
	for _, b := range c.IRCBridges {
		if err := check(b.PasswordFile, true); err != nil {
			return err
		}
		if err := check(b.TLSCAFile, false); err != nil {
			return err
		}
		if err := checkAccounts(b.Accounts); err != nil {
			return err
		}
	}
	for _, bridge := range c.XMPPBridges {
		if err := check(bridge.PasswordFile, true); err != nil {
			return err
		}
		if err := check(bridge.TLSCAFile, false); err != nil {
			return err
		}
		if err := checkAccounts(bridge.Accounts); err != nil {
			return err
		}
	}
	for _, bridge := range c.MatrixBridges {
		if err := check(bridge.AccessTokenFile, true); err != nil {
			return err
		}
		if err := check(bridge.TLSCAFile, false); err != nil {
			return err
		}
		if err := checkAccounts(bridge.Accounts); err != nil {
			return err
		}
	}
	for _, owner := range c.PostgresNotify {
		if err := check(owner.ClientCertFile, false); err != nil {
			return err
		}
		if err := check(owner.ClientKeyFile, true); err != nil {
			return err
		}
		if err := check(owner.TLSCAFile, false); err != nil {
			return err
		}
	}
	if u := c.Upstream; u != nil {
		if err := share(u.AccessTokenFile, true, "password"); err != nil {
			return err
		}
		if err := check(u.GatewayTokenFile, true); err != nil {
			return err
		}
		if err := share(u.TLSCAFile, false, "certificate"); err != nil {
			return err
		}
	}
	for _, l := range c.Listeners {
		if err := share(l.PasswordFile, true, "password"); err != nil {
			return err
		}
	}
	for _, path := range c.boundAuthorFiles() {
		if err := check(path, true); err != nil {
			return err
		}
	}
	binaries := map[string]bool{}
	for _, l := range c.Listeners {
		if l.ChirpStack != nil {
			if err := check(l.ChirpStack.SecretFile, true); err != nil {
				return err
			}
		}
		if l.GitHubWebhook != nil {
			if err := check(l.GitHubWebhook.SecretFile, true); err != nil {
				return err
			}
		}
		if l.SNMP != nil {
			for _, r := range l.SNMP.Routes {
				if err := check(r.AuthenticationKeyFile, true); err != nil {
					return err
				}
			}
		}
		if l.Protocol == "file_mailbox" {
			info, err := os.Lstat(l.Address)
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode().Perm() != 0700 {
				return errors.New("file mailbox must be a private directory")
			}
			paths = append(paths, l.Address)
			for _, name := range []string{"request.json", "processing.json", "response.tmp", "response.json"} {
				paths = append(paths, filepath.Join(l.Address, name))
			}
		}
		if l.OSCORE != nil {
			if err := check(l.OSCORE.ContextFile, true); err != nil {
				return err
			}
			if a := l.OSCORE.Author; a != nil && a.TokenFile != "" {
				if err := check(a.TokenFile, true); err != nil {
					return err
				}
			}
			if !binaries[l.OSCORE.Binary] {
				if err := check(l.OSCORE.Binary, false); err != nil {
					return err
				}
				binaries[l.OSCORE.Binary] = true
			}
		}
		if l.Protocol == "mapped_file" {
			mappedPaths = append(mappedPaths, l.Address, filepath.Join(l.Address, "queue.dat"), filepath.Join(l.Address, "transaction.lock"))
		}
		if err := share(l.TLSKeyFile, true, "key"); err != nil {
			return err
		}
		if err := share(l.TLSCertFile, false, "certificate"); err != nil {
			return err
		}
		if l.WebRTC != nil {
			for _, server := range l.WebRTC.ICEServers {
				if err := check(server.CredentialFile, true); err != nil {
					return err
				}
			}
		}
	}
	// Queue-owned paths participate in identity checks, but are not external
	// references in the containment check below. No path spelling is exempt.
	allPaths := append(append([]string(nil), paths...), mappedPaths...)
	if err := pathcheck.Distinct(allPaths...); err != nil {
		return errors.New("state, credentials and queue resources must not alias each other")
	}
	localReferences := c.mappedFileLocalReferences()
	for _, queuePath := range mappedPaths {
		for _, reference := range localReferences {
			if reference != "" && pathcheck.Same(queuePath, reference) {
				return errors.New("mapped_file resources must not alias another local carrier")
			}
		}
	}
	if err := c.checkMailboxReferences(paths); err != nil {
		return err
	}
	if info, err := os.Lstat(c.StateDir); err == nil {
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return errors.New("state directory must be owner-only and not a symlink")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
