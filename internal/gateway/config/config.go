// Package config defines owner-provisioned gateway configuration. Parsing is
// read-only and does not open listeners, create identities or contact AIRC.
package config

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const MaxBytes = 64 << 10

func UnixAvailable() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "linux" }

func StdioAvailable() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "linux" }

type Config struct {
	Version       int            `json:"version"`
	Realm         string         `json:"realm"`
	StateDir      string         `json:"state_dir"`
	Bindings      []Binding      `json:"bindings"`
	Principals    []Principal    `json:"principals"`
	Listeners     []Listener     `json:"listeners"`
	ReturnRoutes  []ReturnRoute  `json:"return_routes"`
	Limits        Limits         `json:"limits"`
	Retry         Retry          `json:"retry"`
	RecipientKeys []RecipientKey `json:"recipient_keys,omitempty"`
	CursorKeyFile string         `json:"cursor_key_file,omitempty"`
}

type Binding struct {
	ID               string `json:"id"`
	Network          string `json:"network"`
	Address          string `json:"address"`
	Account          string `json:"account"`
	AccountTokenFile string `json:"account_token_file"`
	AccessTokenFile  string `json:"access_token_file,omitempty"`
	TLSServerName    string `json:"tls_server_name,omitempty"`
	TLSCAFile        string `json:"tls_ca_file,omitempty"`
}

type Principal struct {
	ID              string           `json:"id"`
	Binding         string           `json:"binding"`
	BearerTokenFile string           `json:"bearer_token_file"`
	Operations      []core.Operation `json:"operations"`
	Targets         []core.Target    `json:"targets"`
	Revoked         bool             `json:"revoked,omitempty"`
	SigningKeys     []SigningKey     `json:"signing_keys,omitempty"`
}

type SigningKey struct {
	ID            string `json:"id"`
	PublicKeyFile string `json:"public_key_file"`
}

type RecipientKey struct {
	ID             string `json:"id"`
	PrivateKeyFile string `json:"private_key_file"`
}

type Listener struct {
	AllowedOrigins []string         `json:"allowed_origins,omitempty"`
	ID             string           `json:"id"`
	Protocol       string           `json:"protocol"`
	Address        string           `json:"address"`
	Enabled        bool             `json:"enabled"`
	TLSCertFile    string           `json:"tls_cert_file,omitempty"`
	TLSKeyFile     string           `json:"tls_key_file,omitempty"`
	Operations     []core.Operation `json:"operations"`
}

type ReturnRoute struct {
	ID          string `json:"id"`
	Principal   string `json:"principal"`
	Protocol    string `json:"protocol"`
	Destination string `json:"destination"`
}

type Limits struct {
	Workers              int   `json:"workers"`
	Sessions             int   `json:"sessions"`
	SessionsPerPrincipal int   `json:"sessions_per_principal"`
	Watches              int   `json:"watches"`
	WatchesPerPrincipal  int   `json:"watches_per_principal"`
	Pending              int   `json:"pending"`
	PendingPerPrincipal  int   `json:"pending_per_principal"`
	JournalBytes         int64 `json:"journal_bytes"`
}

type Retry struct {
	Policy                   core.Policy `json:"policy"`
	SendAttempts             int         `json:"send_attempts"`
	LookupAttempts           int         `json:"lookup_attempts"`
	LifetimeSeconds          int64       `json:"lifetime_seconds"`
	BackoffSeconds           int         `json:"backoff_seconds"`
	MaxBackoffSeconds        int         `json:"max_backoff_seconds"`
	TerminalRetentionSeconds int64       `json:"terminal_retention_seconds"`
}

func Defaults() Config {
	return Config{Bindings: []Binding{}, Principals: []Principal{}, Listeners: []Listener{}, ReturnRoutes: []ReturnRoute{}, Limits: Limits{Workers: 8, Sessions: 32, SessionsPerPrincipal: 4, Watches: 8, WatchesPerPrincipal: 2, Pending: 4096, PendingPerPrincipal: 128, JournalBytes: 256 << 20}, Retry: Retry{Policy: core.Retry, SendAttempts: 100, LookupAttempts: 100, LifetimeSeconds: 86400, BackoffSeconds: 1, MaxBackoffSeconds: 60, TerminalRetentionSeconds: 7 * 86400}}
}

func Parse(data []byte) (Config, error) {
	c := Defaults()
	if err := jsoninput.Decode(data, &c, MaxBytes); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func id(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func operations(ops []core.Operation) error {
	seen := map[core.Operation]bool{}
	for _, op := range ops {
		if !op.Valid() || seen[op] {
			return errors.New("invalid or duplicate operation")
		}
		seen[op] = true
	}
	return nil
}

func absolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\r\n\x00")
}

func (c *Config) Validate() error {
	if c.Version != 1 || !id(c.Realm) || !absolute(c.StateDir) {
		return errors.New("invalid version, realm, or state directory")
	}
	if c.CursorKeyFile != "" && !absolute(c.CursorKeyFile) {
		return errors.New("invalid cursor key path")
	}
	if len(c.Bindings) > 256 || len(c.Principals) > 1024 || len(c.Listeners) > 128 || len(c.ReturnRoutes) > 1024 {
		return errors.New("configuration count limit exceeded")
	}
	bindings := map[string]bool{}
	for _, b := range c.Bindings {
		if !id(b.ID) || bindings[b.ID] {
			return errors.New("invalid or duplicate binding ID")
		}
		bindings[b.ID] = true
		nick := core.Target{Kind: "direct", Name: b.Account}
		if err := nick.Normalize(); err != nil {
			return fmt.Errorf("binding %s: invalid account", b.ID)
		}
		if !absolute(b.AccountTokenFile) || b.AccessTokenFile != "" && !absolute(b.AccessTokenFile) || b.TLSCAFile != "" && !absolute(b.TLSCAFile) {
			return fmt.Errorf("binding %s: invalid credential path", b.ID)
		}
		switch b.Network {
		case "unix":
			if !absolute(b.Address) || b.TLSServerName != "" || b.TLSCAFile != "" {
				return errors.New("unix binding requires an absolute socket and no TLS settings")
			}
		case "tcp":
			if err := endpoint(b.Address, b.TLSServerName != ""); err != nil {
				return fmt.Errorf("binding %s: %w", b.ID, err)
			}
			if b.TLSServerName == "" && b.TLSCAFile != "" {
				return errors.New("TLS CA requires a server name")
			}
			if b.TLSServerName != "" && b.AccessTokenFile == "" {
				return errors.New("TLS upstream requires an access token file")
			}
		default:
			return errors.New("unsupported backend network")
		}
	}
	principals := map[string]bool{}
	for i := range c.Principals {
		p := &c.Principals[i]
		if !id(p.ID) || principals[p.ID] || !bindings[p.Binding] || (p.BearerTokenFile != "" && !absolute(p.BearerTokenFile)) || p.BearerTokenFile == "" && len(p.SigningKeys) == 0 {
			return errors.New("invalid principal, binding, or credential path")
		}
		principals[p.ID] = true
		if err := operations(p.Operations); err != nil {
			return err
		}
		if len(p.Targets) > 256 {
			return errors.New("principal target limit exceeded")
		}
		seen := map[core.Target]bool{}
		for j := range p.Targets {
			if err := p.Targets[j].Normalize(); err != nil {
				return err
			}
			if seen[p.Targets[j]] {
				return errors.New("duplicate target permission")
			}
			seen[p.Targets[j]] = true
		}
	}
	keyIDs := map[string]bool{}
	for _, p := range c.Principals {
		if len(p.SigningKeys) > 8 {
			return errors.New("principal signing key limit exceeded")
		}
		for _, key := range p.SigningKeys {
			if !id(key.ID) || keyIDs[key.ID] || !absolute(key.PublicKeyFile) {
				return errors.New("invalid or duplicate signing key")
			}
			keyIDs[key.ID] = true
		}
	}
	if len(c.RecipientKeys) > 8 {
		return errors.New("recipient key limit exceeded")
	}
	recipientIDs := map[string]bool{}
	for _, key := range c.RecipientKeys {
		if !id(key.ID) || recipientIDs[key.ID] || !absolute(key.PrivateKeyFile) {
			return errors.New("invalid or duplicate recipient key")
		}
		recipientIDs[key.ID] = true
	}
	listeners, addresses := map[string]bool{}, map[string]bool{}
	stdioEnabled, enabled := 0, 0
	for i := range c.Listeners {
		l := &c.Listeners[i]
		if !id(l.ID) || listeners[l.ID] || l.Protocol == "" {
			return errors.New("invalid or duplicate listener")
		}
		listeners[l.ID] = true
		if err := ValidateOrigins(l.AllowedOrigins); err != nil {
			return err
		}
		if len(l.AllowedOrigins) > 0 && l.Protocol != "http" && l.Protocol != "websocket" {
			return errors.New("allowed origins require HTTP or WebSocket listener")
		}
		if l.Address == "" {
			if l.Protocol == "unix" {
				return errors.New("unix listener requires an absolute socket path")
			}
			if l.Protocol != "stdio" {
				l.Address = "127.0.0.1:0"
			}
		}
		if l.Protocol == "stdio" && (l.Address != "" || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) > 0) {
			return errors.New("stdio listener cannot use address, TLS, or origins")
		}
		if l.Protocol == "unix" && (!absolute(l.Address) || filepath.Clean(l.Address) != l.Address) {
			return errors.New("unix listener requires an absolute normalized socket path")
		}
		if l.Enabled && l.Protocol == "unix" && !UnixAvailable() {
			return errors.New("unix listener is unavailable on this platform")
		}
		if err := operations(l.Operations); err != nil {
			return err
		}
		if (l.TLSCertFile == "") != (l.TLSKeyFile == "") {
			return errors.New("listener requires both TLS certificate and key")
		}
		if l.TLSKeyFile != "" && (!absolute(l.TLSKeyFile) || !absolute(l.TLSCertFile)) {
			return errors.New("invalid listener TLS path")
		}
		if l.Protocol != "unix" && l.Protocol != "stdio" {
			if err := endpoint(l.Address, l.TLSKeyFile != ""); err != nil {
				return err
			}
		}
		if l.Enabled {
			enabled++
			if l.Protocol == "stdio" {
				if !StdioAvailable() {
					return errors.New("stdio listener is unavailable on this platform")
				}
				stdioEnabled++
			}
			if addresses[l.Address] {
				return errors.New("duplicate enabled listener address")
			}
			addresses[l.Address] = true
			if l.Protocol != "http" && l.Protocol != "framed" && l.Protocol != "websocket" && l.Protocol != "unix" && l.Protocol != "stdio" {
				return fmt.Errorf("listener %s: protocol %s is not implemented", l.ID, l.Protocol)
			}
			if l.Protocol == "unix" && l.TLSCertFile != "" {
				return errors.New("unix listener cannot use TLS certificates")
			}
			for _, op := range l.Operations {
				if op == core.Watch && (c.Limits.Watches < 1 || c.Limits.WatchesPerPrincipal < 1) {
					return errors.New("enabled watch requires nonzero global and per-principal watch limits")
				}
				if op != core.Send && op != core.Reply && op != core.ReactionAdd && op != core.ContextRead && op != core.PinsRead && op != core.RoomRead && op != core.WaitingRead && op != core.PollResults && op != core.TopicRead && op != core.ReceiptGet && op != core.HistoryRead && op != core.Search && op != core.Watch && op != core.Directory && op != core.ProfileUpdate && op != core.PresenceSet && op != core.TopicSet && op != core.RoomUpdate && op != core.PinsAdd && op != core.PinsRemove {
					return fmt.Errorf("listener %s: operation %s is not implemented", l.ID, op)
				}
				if (op == core.HistoryRead || op == core.Search || op == core.Watch) && c.CursorKeyFile == "" {
					return errors.New("enabled history/search/watch requires a cursor key file")
				}
				if (op == core.HistoryRead || op == core.Search || op == core.Watch || op == core.Directory || op == core.ContextRead || op == core.PinsRead || op == core.RoomRead || op == core.WaitingRead || op == core.PollResults || op == core.TopicRead) && (c.Limits.Sessions < 2 || c.Limits.SessionsPerPrincipal < 2) {
					return errors.New("enabled history/search/watch/directory/context/pins/room/waiting/poll/topic requires at least two global and per-principal sessions")
				}
			}
		}
	}
	if stdioEnabled > 0 && enabled != 1 {
		return errors.New("stdio requires exactly one enabled listener")
	}
	routes := map[string]bool{}
	for _, r := range c.ReturnRoutes {
		if !id(r.ID) || routes[r.ID] || !principals[r.Principal] || r.Protocol == "" || r.Destination == "" || len(r.Destination) > 2048 || strings.ContainsAny(r.Destination, "\r\n\x00") {
			return errors.New("invalid return route")
		}
		routes[r.ID] = true
	}
	return c.validateLimits()
}

func endpoint(address string, tls bool) error {
	host, port, err := net.SplitHostPort(address)
	p, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || p < 0 || p > 65535 {
		return errors.New("invalid network endpoint")
	}
	// Only numeric addresses are accepted: DNS must not turn a local plaintext
	// credential-bearing connection into a remote one after validation.
	ip := net.ParseIP(host)
	if ip == nil {
		return errors.New("endpoint requires an explicit numeric IP")
	}
	if !ip.IsLoopback() && !tls {
		return errors.New("non-loopback endpoint requires verified TLS")
	}
	return nil
}
