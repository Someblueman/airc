// Package service manages an owned per-user aircd job. It never changes firewall
// rules, installs a system-wide daemon, or removes chat state.
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$`)

type Config struct {
	Name     string   `json:"name"`
	Binary   string   `json:"binary"`
	StateDir string   `json:"state_dir"`
	Args     []string `json:"args"`
}

func (c Config) Label() string {
	if c.Name == "default" {
		return "local.airc"
	}
	return "local.airc." + c.Name
}

func (c Config) Validate() error {
	if !validName.MatchString(c.Name) {
		return errors.New("service name must be 1-40 letters, digits, underscores or hyphens")
	}
	for _, s := range append([]string{c.Binary, c.StateDir}, c.Args...) {
		for _, r := range s {
			if r < 32 || r == 127 {
				return errors.New("service paths and arguments cannot contain control characters")
			}
		}
	}
	if !filepath.IsAbs(c.Binary) || !filepath.IsAbs(c.StateDir) {
		return errors.New("service binary and state directory must be absolute paths")
	}
	return nil
}

func (c Config) Manifest() (string, error) { return c.manifest(runtime.GOOS) }
func (c Config) manifest(platform string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch platform {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", c.Label()+".plist"), nil
	case "linux":
		dir := os.Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".config")
		}
		if !filepath.IsAbs(dir) {
			return "", errors.New("XDG_CONFIG_HOME must be absolute")
		}
		return filepath.Join(dir, "systemd", "user", c.Label()+".service"), nil
	default:
		return "", fmt.Errorf("service management is not supported on %s", platform)
	}
}

func Load(dir, name string) (Config, error) {
	var c Config
	f, err := os.Open(filepath.Join(dir, "service.json"))
	if err != nil {
		return c, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 32768 {
		return c, errors.New("service config must be an owner-only regular file of at most 32 KiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 32769))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return c, errors.New("trailing data in service config")
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	if c.Name != name || c.StateDir != dir {
		return c, errors.New("service config belongs to another name or state directory")
	}
	return c, nil
}

// Install uses exclusive creation so an existing job is never silently replaced.
func Install(c Config) error {
	path, err := c.Manifest()
	if err != nil {
		return err
	}
	data, err := render(c, runtime.GOOS)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(c.StateDir, 0700); err != nil {
		return err
	}
	configPath := filepath.Join(c.StateDir, "service.json")
	encoded, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := createFile(configPath, encoded); err != nil {
		return fmt.Errorf("save service config: %w", err)
	}
	if err := createFile(path, data); err != nil {
		_ = os.Remove(configPath)
		return fmt.Errorf("install service definition: %w", err)
	}
	return nil
}

func createFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if err := errors.Join(writeErr, f.Close()); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
