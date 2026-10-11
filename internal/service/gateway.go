package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	gwconfig "github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/protocol"
)

// Gateway is the bundled airc-gateway: its own process and process-manager
// job, managed together with aircd as one service. Its configuration,
// credentials, upstream and state are all derived from the service.
type Gateway struct {
	Binary   string    `json:"binary"`
	Carriers []Carrier `json:"carriers"`
	// UpstreamServerName verifies aircd's certificate when aircd uses TLS.
	UpstreamServerName string `json:"upstream_server_name,omitempty"`
}

// Carrier is one enabled gateway listener, in the operator's preference order.
type Carrier struct {
	Name    string `json:"name"`    // gateway protocol, e.g. "http"
	Address string `json:"address"` // HOST:PORT, or a Unix socket path
}

type carrierSpec struct {
	name string
	port int // default TCP port; zero for a Unix socket
}

// carrierSpecs are the carriers install can configure with no settings beyond
// an address. A carrier is offered only while the gateway accepts plain author
// requests on it (gwconfig.AuthorCarrier).
var carrierSpecs = []carrierSpec{
	{"http", 6680}, {"websocket", 6681}, {"mcp_http", 6682}, {"framed", 6683}, {"unix", 0},
}

// SupportedCarriers lists the carrier names install accepts.
func SupportedCarriers() []string {
	var names []string
	for _, spec := range carrierSpecs {
		if gwconfig.AuthorCarrier(spec.name) {
			names = append(names, spec.name)
		}
	}
	return names
}

// GatewayLabel cannot collide with any aircd label: service names have no dots
// and "local.airc-gateway" is not "local.airc." plus a name.
func (c Config) GatewayLabel() string {
	if c.Name == "default" {
		return "local.airc-gateway"
	}
	return "local.airc-gateway." + c.Name
}

// Generated gateway files inside the service state directory. The config is
// replaced by each install; the credentials and journal are kept.
func (c Config) GatewayConfigPath() string { return filepath.Join(c.StateDir, "gateway.json") }
func (c Config) GatewayTokenPath() string  { return filepath.Join(c.StateDir, "gateway.token") }
func (c Config) GatewayCursorKeyPath() string {
	return filepath.Join(c.StateDir, "gateway-cursor.key")
}
func (c Config) GatewayStateDir() string { return filepath.Join(c.StateDir, "gateway") }
func (c Config) GatewayLogPath() string  { return filepath.Join(c.StateDir, "gateway-startup.log") }

// ParseCarriers reads --gateway: comma-separated carrier names, each
// optionally NAME=PORT or NAME=HOST:PORT (NAME=/path for unix). Carriers
// without a host bind host; the unix carrier defaults to DIR/gateway.sock.
func ParseCarriers(spec, host, dir string) ([]Carrier, error) {
	var carriers []Carrier
	names, addresses := map[string]bool{}, map[string]bool{}
	for item := range strings.SplitSeq(spec, ",") {
		name, value, explicit := strings.Cut(strings.TrimSpace(item), "=")
		index := slices.IndexFunc(carrierSpecs, func(s carrierSpec) bool { return s.name == name })
		if index < 0 || !gwconfig.AuthorCarrier(name) {
			if index >= 0 {
				return nil, fmt.Errorf("gateway carrier %q does not take plain author requests in this build; supported: %s", name, strings.Join(SupportedCarriers(), ", "))
			}
			return nil, fmt.Errorf("unknown gateway carrier %q; supported: %s", name, strings.Join(SupportedCarriers(), ", "))
		}
		if names[name] {
			return nil, fmt.Errorf("gateway carrier %q listed twice", name)
		}
		names[name] = true
		s := carrierSpecs[index]
		address := ""
		if s.port == 0 {
			address = filepath.Join(dir, "gateway.sock")
			if explicit {
				address = value
			}
			if !filepath.IsAbs(address) || filepath.Clean(address) != address {
				return nil, fmt.Errorf("gateway carrier %s needs an absolute socket path", name)
			}
		} else {
			address = net.JoinHostPort(host, strconv.Itoa(s.port))
			if explicit {
				address = value
				if port, err := strconv.Atoi(value); err == nil {
					address = net.JoinHostPort(host, strconv.Itoa(port))
				}
			}
			h, p, err := net.SplitHostPort(address)
			port, portErr := strconv.Atoi(p)
			if err != nil || portErr != nil || port < 1 || port > 65535 || net.ParseIP(h) == nil {
				return nil, fmt.Errorf("gateway carrier %s: use %s=PORT or %s=IP:PORT with a numeric IP and port 1-65535", name, name, name)
			}
		}
		if addresses[address] {
			return nil, fmt.Errorf("gateway carriers share address %s", address)
		}
		addresses[address] = true
		carriers = append(carriers, Carrier{Name: name, Address: address})
	}
	return carriers, nil
}

// daemonFlag is the value of one of aircd's flags in the service arguments.
func (c Config) daemonFlag(name string) string {
	for i := 0; i+1 < len(c.Args); i++ {
		if c.Args[i] == name {
			return c.Args[i+1]
		}
	}
	return ""
}

// Discovery is the ordered carrier list published by aircd (005 CARRIERS and
// STATUS) and the gateway (capabilities.get): aircd's own IRC endpoint first,
// then each gateway carrier in the operator's order. Ports are on the host the
// client already reaches; Unix socket paths are local.
func (c Config) Discovery() ([]protocol.Carrier, error) {
	if c.Gateway == nil {
		return nil, nil
	}
	tls := c.daemonFlag("--tls-cert") != ""
	var list []protocol.Carrier
	add := func(name, address string, tls bool) error {
		entry := protocol.Carrier{Name: name, TLS: tls, Path: address}
		if !filepath.IsAbs(address) {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			entry.Path = ""
			entry.Port, _ = strconv.Atoi(port)
		}
		if _, err := protocol.ParseCarrier(entry.String()); err != nil {
			return fmt.Errorf("carrier %s at %s cannot be advertised (choose another socket path or port): %w", name, address, err)
		}
		list = append(list, entry)
		return nil
	}
	if unix := c.daemonFlag("--unix"); unix != "" {
		if err := add("irc", unix, false); err != nil {
			return nil, err
		}
	} else if err := add("irc", c.daemonFlag("--listen"), tls); err != nil {
		return nil, err
	}
	for _, carrier := range c.Gateway.Carriers {
		if err := add(carrier.Name, carrier.Address, tls && carrier.Name != "unix"); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// upstream is the gateway's hop to aircd: its Unix socket, or its TCP port on
// loopback (with TLS and the access token when aircd requires them).
func (c Config) upstream() (gwconfig.Upstream, error) {
	u := gwconfig.Upstream{GatewayTokenFile: c.GatewayTokenPath(), AccessTokenFile: c.daemonFlag("--access-token-file")}
	if unix := c.daemonFlag("--unix"); unix != "" {
		u.Network, u.Address = "unix", unix
		return u, nil
	}
	host, port, err := net.SplitHostPort(c.daemonFlag("--listen"))
	if err != nil {
		return u, err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return u, errors.New("aircd listen address must be a numeric IP to bundle the gateway")
	}
	if !ip.IsLoopback() {
		host = "127.0.0.1"
		if ip.To4() == nil {
			host = "::1"
		}
	}
	u.Network, u.Address = "tcp", net.JoinHostPort(host, port)
	if cert := c.daemonFlag("--tls-cert"); cert != "" {
		u.TLSServerName, u.TLSCAFile = c.Gateway.UpstreamServerName, cert
	}
	return u, nil
}

// gatewayFile is the generated gateway configuration. Omitted settings take
// the gateway's defaults; there are no principals, bindings or keys.
type gatewayFile struct {
	Version       int               `json:"version"`
	Realm         string            `json:"realm"`
	StateDir      string            `json:"state_dir"`
	CursorKeyFile string            `json:"cursor_key_file"`
	Upstream      gwconfig.Upstream `json:"upstream"`
	Carriers      []string          `json:"carriers"`
	Listeners     []gatewayListener `json:"listeners"`
}

type gatewayListener struct {
	ID           string   `json:"id"`
	Protocol     string   `json:"protocol"`
	Address      string   `json:"address"`
	Enabled      bool     `json:"enabled"`
	Operations   []string `json:"operations"`
	TLSCertFile  string   `json:"tls_cert_file,omitempty"`
	TLSKeyFile   string   `json:"tls_key_file,omitempty"`
	PasswordFile string   `json:"password_file,omitempty"`
}

// GatewayConfig renders the gateway configuration. Listeners take every
// operation and guests, as IRC does. When aircd uses TLS, TCP carriers reuse
// its certificate; when aircd requires its access token, every carrier
// requires the same token as its password (IRC's server PASS), so the gateway
// never widens who may use the service.
func (c Config) GatewayConfig() ([]byte, error) {
	if c.Gateway == nil {
		return nil, errors.New("service has no gateway")
	}
	upstream, err := c.upstream()
	if err != nil {
		return nil, err
	}
	discovery, err := c.Discovery()
	if err != nil {
		return nil, err
	}
	file := gatewayFile{Version: 1, Realm: c.Name, StateDir: c.GatewayStateDir(), CursorKeyFile: c.GatewayCursorKeyPath(), Upstream: upstream, Listeners: []gatewayListener{}}
	for _, entry := range discovery {
		file.Carriers = append(file.Carriers, entry.String())
	}
	cert, key, access := c.daemonFlag("--tls-cert"), c.daemonFlag("--tls-key"), c.daemonFlag("--access-token-file")
	for _, carrier := range c.Gateway.Carriers {
		l := gatewayListener{ID: carrier.Name, Protocol: carrier.Name, Address: carrier.Address, Enabled: true, Operations: []string{}, PasswordFile: access}
		if cert != "" && carrier.Name != "unix" {
			l.TLSCertFile, l.TLSKeyFile = cert, key
		}
		file.Listeners = append(file.Listeners, l)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := gwconfig.Parse(data); err != nil {
		return nil, fmt.Errorf("generated gateway configuration is invalid: %w", err)
	}
	return data, nil
}

// validateGateway asks the installed gateway to validate its configuration,
// so an older or newer airc-gateway binary is caught at install.
var validateGateway = func(binary, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "config", "validate", "--file", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("airc-gateway rejected the generated configuration: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// PrivateDirectory reports whether path's parent is an owner-only directory,
// which the gateway requires for every private credential it reads.
func PrivateDirectory(path string) error { return directoryMode(path, 0077, "owner-only (chmod 700)") }

// PublicDirectory reports whether path's parent is writable only by its
// owner, which the gateway requires for a certificate it reads.
func PublicDirectory(path string) error {
	return directoryMode(path, 0022, "writable only by its owner")
}

func directoryMode(path string, mask os.FileMode, want string) error {
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&mask != 0 {
		return fmt.Errorf("%s must be a directory %s to hold files the gateway reads", dir, want)
	}
	return nil
}
