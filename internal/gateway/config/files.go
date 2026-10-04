package config

import (
	"errors"
	"fmt"
	"io"
	"os"

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
	for _, l := range c.Listeners {
		if err := check(l.TLSKeyFile, true); err != nil {
			return err
		}
		if err := check(l.TLSCertFile, false); err != nil {
			return err
		}
	}
	if err := pathcheck.Distinct(paths...); err != nil {
		return errors.New("state and credential references must not alias each other")
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
