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
	c, err := Parse(data)
	if err != nil {
		return Config{}, err
	}
	return c, c.CheckFiles()
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
		if err := check(owner.Route.RecipientPublicKeyFile, false); err != nil {
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
		for _, path := range []string{u.AccessTokenFile, u.GatewayTokenFile} {
			if err := check(path, true); err != nil {
				return err
			}
		}
		if err := check(u.TLSCAFile, false); err != nil {
			return err
		}
	}
	for _, l := range c.Listeners {
		if err := check(l.PasswordFile, true); err != nil {
			return err
		}
	}
	for _, b := range c.Bindings {
		for _, path := range []string{b.AccountTokenFile, b.AccessTokenFile} {
			if err := check(path, true); err != nil {
				return err
			}
		}
		if err := check(b.TLSCAFile, false); err != nil {
			return err
		}
	}
	for _, p := range c.Principals {
		if err := check(p.BearerTokenFile, true); err != nil {
			return err
		}
		for _, key := range p.SigningKeys {
			if err := check(key.PublicKeyFile, false); err != nil {
				return err
			}
		}
	}
	for _, key := range c.RecipientKeys {
		if err := check(key.PrivateKeyFile, true); err != nil {
			return err
		}
	}
	if c.ResponseSigningKey != nil {
		if err := check(c.ResponseSigningKey.PrivateKeyFile, true); err != nil {
			return err
		}
	}
	for _, route := range c.ReturnRoutes {
		if route.RecipientPublicKeyFile != "" {
			if err := check(route.RecipientPublicKeyFile, false); err != nil {
				return err
			}
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
		if err := check(l.TLSKeyFile, true); err != nil {
			return err
		}
		if err := check(l.TLSCertFile, false); err != nil {
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
	for _, route := range c.ReturnRoutes {
		if route.Protocol == "mapped_file" {
			mappedPaths = append(mappedPaths, route.Destination, filepath.Join(route.Destination, "queue.dat"), filepath.Join(route.Destination, "transaction.lock"))
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
