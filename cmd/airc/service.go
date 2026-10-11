package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/pathcheck"
	"github.com/Someblueman/airc/internal/protocol"
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
		installed := c.Label()
		if c.Gateway != nil {
			installed += " and " + c.GatewayLabel()
		}
		_, err = fmt.Fprintf(stdout, "Installed %s. Start with airc service start --name %s --state-dir %q\n", installed, c.Name, c.StateDir)
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
		if err := gatewayReady(c); err != nil {
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
	return printServiceStatus(stdout, c, result)
}

func printServiceStatus(w io.Writer, c service.Config, result service.Status) error {
	_, err := fmt.Fprintf(w, "%s: running=%t loaded=%t pid=%d\n", c.Label(), result.Running, result.Loaded, result.PID)
	if g := result.Gateway; err == nil && g != nil {
		_, err = fmt.Fprintf(w, "%s: running=%t loaded=%t pid=%d\nCarriers (client preference order): %s\n", c.GatewayLabel(), g.Running, g.Loaded, g.PID, strings.Join(result.Carriers, " "))
	}
	if err == nil {
		_, err = fmt.Fprintf(w, "Definition: %s\n", result.Definition)
	}
	if g := result.Gateway; err == nil && g != nil {
		_, err = fmt.Fprintf(w, "Gateway definition: %s\nGateway config: %s\n", g.Definition, c.GatewayConfigPath())
	}
	if err == nil {
		_, err = fmt.Fprintf(w, "Data: %s\n", result.StateDir)
	}
	return err
}

type serviceInstallOptions struct {
	binary, listen, unix, cert, key, access string
	sync                                    string
	history, connections, messageSize       int
	gateway, gatewayBinary                  string
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
	fs.StringVar(&o.gateway, "gateway", "", "also run the bundled gateway with these carriers, e.g. http,websocket=8081 ("+strings.Join(service.SupportedCarriers(), ", ")+")")
	fs.StringVar(&o.gatewayBinary, "gateway-binary", "", "airc-gateway executable (default: beside airc, then PATH)")
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
	var err error
	if c.Binary, err = siblingBinary(o.binary, "aircd"); err != nil {
		return c, err
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
		if !pathcheck.OwnerOnly(info) {
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
	gatewayFiles, err := o.bundleGateway(&c)
	if err != nil {
		return c, err
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	// Check every exclusive destination before creating any credential.
	path, _ := c.Manifest()
	destinations := []string{path, filepath.Join(dir, "service.json")}
	if c.Gateway != nil {
		gatewayPath, err := c.GatewayManifest()
		if err != nil {
			return c, err
		}
		destinations = append(destinations, gatewayPath, c.GatewayConfigPath())
		gatewayFiles = append(gatewayFiles, gatewayPath)
	}
	for _, path := range destinations {
		if _, err := os.Lstat(path); err == nil {
			return c, fmt.Errorf("service already installed at %s; uninstall first (data is preserved)", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return c, err
		}
	}
	tokenPath := filepath.Join(dir, "admin.token")
	history := filepath.Join(dir, "history.jsonl")
	if err := pathcheck.Distinct(append([]string{c.Binary, path, filepath.Join(dir, "service.json"), tokenPath, history, history + ".accounts.json", history + ".chat.json", history + ".topics.json", history + ".profiles.json", history + ".moderation.json", filepath.Join(dir, "service.log"), filepath.Join(dir, "service.log.1"), filepath.Join(dir, "startup.log"), o.cert, o.key, o.access, o.unix}, gatewayFiles...)...); err != nil {
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
	roles := map[string]string{operator: "admin"}
	if o.access != "" {
		access, err := admin.ReadToken(o.access)
		if err != nil {
			return c, err
		}
		if roles[access] != "" {
			return c, errors.New("access and admin credentials must be different")
		}
		roles[access] = "access"
	}
	if c.Gateway != nil {
		return c, gatewayCredentials(c, roles)
	}
	return c, nil
}

// bundleGateway adds the gateway to an aircd service config: its binary and
// carriers, aircd's gateway credential and the advertised carrier list. It
// returns the gateway's own files for the distinct-path check.
func (o serviceInstallOptions) bundleGateway(c *service.Config) ([]string, error) {
	if o.gateway == "" {
		if o.gatewayBinary != "" {
			return nil, errors.New("--gateway-binary requires --gateway")
		}
		return nil, nil
	}
	binary, err := siblingBinary(o.gatewayBinary, "airc-gateway")
	if err != nil {
		return nil, err
	}
	host := "127.0.0.1"
	if o.unix == "" {
		addr, err := net.ResolveTCPAddr("tcp", o.listen)
		if err != nil {
			return nil, err
		}
		if addr.IP == nil {
			addr.IP = net.IPv4zero
		}
		host = addr.IP.String()
		// The service runs aircd at the resolved address the gateway dials.
		c.Args[1] = net.JoinHostPort(host, strconv.Itoa(addr.Port))
	}
	carriers, err := service.ParseCarriers(o.gateway, host, c.StateDir)
	if err != nil {
		return nil, fmt.Errorf("--gateway: %w", err)
	}
	c.Gateway = &service.Gateway{Binary: binary, Carriers: carriers}
	files := []string{binary, c.GatewayConfigPath(), c.GatewayTokenPath(), c.GatewayCursorKeyPath(), c.GatewayStateDir(), c.GatewayLogPath()}
	for _, carrier := range carriers {
		if filepath.IsAbs(carrier.Address) { // a Unix socket
			files = append(files, carrier.Address)
		} else if o.unix == "" {
			_, port, _ := net.SplitHostPort(carrier.Address)
			if _, own, _ := net.SplitHostPort(c.Args[1]); port == own {
				return nil, fmt.Errorf("--gateway: carrier %s uses aircd's port %s", carrier.Name, port)
			}
		}
	}
	if o.cert != "" {
		if c.Gateway.UpstreamServerName, err = certificateName(o.cert); err != nil {
			return nil, err
		}
	}
	discovery, err := c.Discovery()
	if err != nil {
		return nil, fmt.Errorf("--gateway: %w", err)
	}
	c.Args = append(c.Args, "--gateway-token-file", c.GatewayTokenPath(), "--carriers", protocol.FormatCarriers(discovery))
	return files, nil
}

// gatewayCredentials creates the gateway credential and cursor key once,
// keeps each role's credential distinct, and checks that the gateway can read
// every private file it is given: its strict reader needs owner-only parents.
func gatewayCredentials(c service.Config, roles map[string]string) error {
	for _, path := range []string{c.GatewayTokenPath(), c.GatewayCursorKeyPath()} {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if err := admin.CreateToken(path); err != nil {
				return err
			}
		}
	}
	gateway, err := admin.ReadToken(c.GatewayTokenPath())
	if err != nil {
		return fmt.Errorf("gateway token: %w", err)
	}
	if roles[gateway] != "" {
		return fmt.Errorf("gateway and %s credentials must be different", roles[gateway])
	}
	if _, err := admin.ReadToken(c.GatewayCursorKeyPath()); err != nil {
		return fmt.Errorf("gateway cursor key: %w", err)
	}
	private := []string{c.GatewayTokenPath()}
	for i := 0; i+1 < len(c.Args); i++ {
		if c.Args[i] == "--access-token-file" || c.Args[i] == "--tls-key" {
			private = append(private, c.Args[i+1])
		}
	}
	for _, path := range private {
		if err := service.PrivateDirectory(path); err != nil {
			return err
		}
	}
	for i := 0; i+1 < len(c.Args); i++ {
		if c.Args[i] == "--tls-cert" {
			return service.PublicDirectory(c.Args[i+1])
		}
	}
	return nil
}

// siblingBinary resolves an executable: the given path, else beside airc,
// else on PATH.
func siblingBinary(path, name string) (string, error) {
	if path == "" {
		self, err := os.Executable()
		if err != nil {
			return "", err
		}
		path = filepath.Join(filepath.Dir(self), name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if path, err = exec.LookPath(name); err != nil {
				return "", err
			}
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("%s must be an executable regular file", name)
	}
	return path, nil
}

// certificateName is the first name in a certificate, which verifies it.
func certificateName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", errors.New("invalid service certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	if len(cert.DNSNames) > 0 {
		return cert.DNSNames[0], nil
	}
	if len(cert.IPAddresses) > 0 {
		return cert.IPAddresses[0].String(), nil
	}
	return "", errors.New("service certificate names no DNS name or IP address")
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
