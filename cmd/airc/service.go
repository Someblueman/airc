package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/pathcheck"
	"github.com/Someblueman/airc/internal/service"
)

func runService(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("use airc service install|start|stop|restart|status|uninstall|token init (see docs/SERVICE.md)")
	}
	if args[0] == "token" {
		return serviceToken(args[1:], stdout, stderr)
	}
	action := args[0]
	switch action {
	case "install", "start", "stop", "restart", "status", "uninstall":
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
	fs := flag.NewFlagSet("airc service "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "default", "per-user service name")
	dir := fs.String("state-dir", "", "service data directory (default: AIRC_STATE_DIR or ~/.local/state/airc)")
	jsonOutput := fs.Bool("json", false, "machine-readable status")
	var install serviceInstallOptions
	if action == "install" {
		install.flags(fs)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected service arguments")
	}
	if *dir == "" {
		value, err := stateDir()
		if err != nil {
			return err
		}
		*dir = value
	}
	absolute, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if action == "install" {
		c, err := install.config(*name, absolute)
		if err != nil {
			return err
		}
		if err := service.Install(c); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Installed %s. Start with airc service start --name %s --state-dir %q\n", c.Label(), c.Name, c.StateDir)
		return err
	}
	c, err := service.Load(absolute, *name)
	if errors.Is(err, os.ErrNotExist) && action == "status" {
		result := service.Status{Name: *name, StateDir: absolute}
		if *jsonOutput {
			return json.NewEncoder(stdout).Encode(result)
		}
		_, err = fmt.Fprintln(stdout, "Service is not installed.")
		return err
	}
	if err != nil {
		return err
	}
	switch action {
	case "start":
		err = service.Start(c)
	case "stop":
		err = service.Stop(c)
	case "restart":
		if err = service.Stop(c); err == nil {
			err = service.Start(c)
		}
	case "uninstall":
		err = service.Uninstall(c)
	}
	if err != nil {
		return err
	}
	if action == "start" || action == "restart" {
		if err := serviceReady(c); err != nil {
			return err
		}
	}
	if action == "uninstall" {
		_, err := fmt.Fprintln(stdout, "Service removed; chat data and credentials preserved.")
		return err
	}
	result, err := service.Inspect(c)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(stdout).Encode(result)
	}
	_, err = fmt.Fprintf(stdout, "%s: running=%t loaded=%t pid=%d\nDefinition: %s\nData: %s\n", c.Label(), result.Running, result.Loaded, result.PID, result.Definition, result.StateDir)
	return err
}

type serviceInstallOptions struct {
	binary, listen, unix, cert, key, access string
	sync                                    string
	history, connections, messageSize       int
}

func (o *serviceInstallOptions) flags(fs *flag.FlagSet) {
	fs.StringVar(&o.binary, "binary", "", "aircd executable (default: beside airc, then PATH)")
	fs.StringVar(&o.listen, "listen", "127.0.0.1:6667", "TCP bind address; remote requires TLS and access token")
	fs.StringVar(&o.unix, "unix", "", "Unix socket instead of TCP")
	fs.StringVar(&o.cert, "tls-cert", "", "TLS certificate chain PEM")
	fs.StringVar(&o.key, "tls-key", "", "owner-only TLS private key PEM")
	fs.StringVar(&o.access, "access-token-file", "", "owner-only connection credential")
	fs.IntVar(&o.history, "history", 1000, "retained messages (1-10000), persisted on disk")
	fs.IntVar(&o.connections, "max-connections", 512, "simultaneous clients (1-1024)")
	fs.IntVar(&o.messageSize, "max-message-size", 4096, "message body bytes (1-4096)")
	fs.StringVar(&o.sync, "sync", "full", "write durability: full (survives power loss), fsync (survives an OS crash; much faster on macOS) or none (survives a daemon crash)")
}

// syncArgument is empty for the daemon's own default, so a managed service
// follows it unless the operator chose otherwise.
func syncArgument(mode string) string {
	if mode == "full" {
		return ""
	}
	return mode
}

func absolutePath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	return filepath.Abs(path)
}

func (o serviceInstallOptions) config(name, dir string) (service.Config, error) {
	c := service.Config{Name: name, StateDir: dir}
	if o.binary == "" {
		self, err := os.Executable()
		if err != nil {
			return c, err
		}
		o.binary = filepath.Join(filepath.Dir(self), "aircd")
		if _, err := os.Stat(o.binary); errors.Is(err, os.ErrNotExist) {
			o.binary, err = exec.LookPath("aircd")
			if err != nil {
				return c, err
			}
		}
	}
	var err error
	c.Binary, err = filepath.Abs(o.binary)
	if err != nil {
		return c, err
	}
	info, err := os.Stat(c.Binary)
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return c, errors.New("aircd binary must be an executable regular file")
	}
	if _, err := c.Manifest(); err != nil {
		return c, err
	}
	if o.sync == "" {
		o.sync = "full"
	}
	if _, err := atomicfile.ParseSync(o.sync); err != nil {
		return c, fmt.Errorf("--sync: %w", err)
	}
	if o.history < 1 || o.history > 10000 || o.connections < 1 || o.connections > 1024 || o.messageSize < 1 || o.messageSize > 4096 {
		return c, errors.New("invalid history, connection or message-size limit")
	}
	for _, ptr := range []*string{&o.unix, &o.cert, &o.key, &o.access} {
		*ptr, err = absolutePath(*ptr)
		if err != nil {
			return c, err
		}
	}
	if o.cert != "" || o.key != "" {
		if o.cert == "" || o.key == "" || o.unix != "" {
			return c, errors.New("TLS requires a certificate, key and TCP listener")
		}
		info, err := os.Stat(o.key)
		if err != nil {
			return c, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return c, errors.New("TLS key must be owner-only (chmod 600)")
		}
		if _, err := tls.LoadX509KeyPair(o.cert, o.key); err != nil {
			return c, err
		}
	}
	if o.access != "" {
		if _, err := admin.ReadToken(o.access); err != nil {
			return c, fmt.Errorf("access token: %w", err)
		}
	}
	if o.unix == "" {
		addr, err := net.ResolveTCPAddr("tcp", o.listen)
		if err != nil {
			return c, err
		}
		if !addr.IP.IsLoopback() && (o.cert == "" || o.access == "") {
			return c, errors.New("remote listeners require --tls-cert, --tls-key and --access-token-file")
		}
	}
	c.Args = []string{"--listen", o.listen, "--history", strconv.Itoa(o.history), "--history-file", filepath.Join(dir, "history.jsonl"), "--admin-token-file", filepath.Join(dir, "admin.token"), "--max-connections", strconv.Itoa(o.connections), "--max-message-size", strconv.Itoa(o.messageSize), "--log-file", filepath.Join(dir, "service.log")}
	for _, pair := range [][2]string{{"--unix", o.unix}, {"--tls-cert", o.cert}, {"--tls-key", o.key}, {"--access-token-file", o.access}, {"--sync", syncArgument(o.sync)}} {
		if pair[1] != "" {
			c.Args = append(c.Args, pair[0], pair[1])
		}
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	// Check both exclusive destinations before creating any credential.
	path, _ := c.Manifest()
	for _, path := range []string{path, filepath.Join(dir, "service.json")} {
		if _, err := os.Lstat(path); err == nil {
			return c, fmt.Errorf("service already installed at %s; uninstall first (data is preserved)", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return c, err
		}
	}
	tokenPath := filepath.Join(dir, "admin.token")
	history := filepath.Join(dir, "history.jsonl")
	if err := pathcheck.Distinct(c.Binary, path, filepath.Join(dir, "service.json"), tokenPath, history, history+".accounts.json", history+".chat.json", history+".topics.json", history+".profiles.json", history+".moderation.json", filepath.Join(dir, "service.log"), filepath.Join(dir, "service.log.1"), filepath.Join(dir, "startup.log"), o.cert, o.key, o.access); err != nil {
		return c, err
	}
	if _, err := os.Stat(tokenPath); errors.Is(err, os.ErrNotExist) {
		if err := admin.CreateToken(tokenPath); err != nil {
			return c, err
		}
	}
	operator, err := admin.ReadToken(tokenPath)
	if err != nil {
		return c, err
	}
	if o.access != "" {
		access, err := admin.ReadToken(o.access)
		if err != nil {
			return c, err
		}
		if access == operator {
			return c, errors.New("access and admin credentials must be different")
		}
	}
	return c, nil
}

func serviceToken(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "init" {
		return errors.New("use airc service token init [--file PATH]")
	}
	fs := flag.NewFlagSet("airc service token init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("file", "", "new connection credential file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected token arguments")
	}
	if *path == "" {
		dir, err := stateDir()
		if err != nil {
			return err
		}
		*path = filepath.Join(dir, "access.token")
	}
	if err := admin.CreateToken(*path); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "Connection credential created:", *path)
	return err
}
