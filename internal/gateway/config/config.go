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
	Version            int                 `json:"version"`
	Realm              string              `json:"realm"`
	StateDir           string              `json:"state_dir"`
	Bindings           []Binding           `json:"bindings"`
	Principals         []Principal         `json:"principals"`
	Listeners          []Listener          `json:"listeners"`
	ReturnRoutes       []ReturnRoute       `json:"return_routes"`
	Limits             Limits              `json:"limits"`
	Retry              Retry               `json:"retry"`
	RecipientKeys      []RecipientKey      `json:"recipient_keys,omitempty"`
	ResponseSigningKey *ResponseSigningKey `json:"response_signing_key,omitempty"`
	CursorKeyFile      string              `json:"cursor_key_file,omitempty"`
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

type ResponseSigningKey struct {
	ID             string `json:"id"`
	PrivateKeyFile string `json:"private_key_file"`
}

type Listener struct {
	AllowedOrigins   []string          `json:"allowed_origins,omitempty"`
	ID               string            `json:"id"`
	Protocol         string            `json:"protocol"`
	Address          string            `json:"address"`
	Enabled          bool              `json:"enabled"`
	TLSCertFile      string            `json:"tls_cert_file,omitempty"`
	TLSKeyFile       string            `json:"tls_key_file,omitempty"`
	Operations       []core.Operation  `json:"operations"`
	WebRTC           *WebRTC           `json:"webrtc,omitempty"`
	OSCORE           *OSCORE           `json:"oscore,omitempty"`
	DirectoryMailbox *DirectoryMailbox `json:"directory_mailbox,omitempty"`
	AppendLog        *AppendLog        `json:"append_log,omitempty"`
}

type ReturnRoute struct {
	ID                     string `json:"id"`
	Principal              string `json:"principal"`
	Protocol               string `json:"protocol"`
	Destination            string `json:"destination"`
	Listener               string `json:"listener,omitempty"`
	RecipientKeyID         string `json:"recipient_key_id,omitempty"`
	RecipientPublicKeyFile string `json:"recipient_public_key_file,omitempty"`
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
	if c.ResponseSigningKey != nil && (!id(c.ResponseSigningKey.ID) || !absolute(c.ResponseSigningKey.PrivateKeyFile)) {
		return errors.New("invalid response signing key")
	}
	listeners, addresses := map[string]bool{}, map[string]bool{}
	udpListeners := map[string]string{}
	udpProtocols := map[string]string{}
	coapTCPListeners := map[string]bool{}
	coapTCPProtocols := map[string]bool{}
	unixgramListeners := map[string]bool{}
	unixgramProtocols := map[string]bool{}
	stdioEnabled, fileMailboxEnabled, enabled := 0, 0, 0
	for i := range c.Listeners {
		l := &c.Listeners[i]
		if !id(l.ID) || listeners[l.ID] || l.Protocol == "" {
			return errors.New("invalid or duplicate listener")
		}
		listeners[l.ID] = true
		if err := ValidateOrigins(l.AllowedOrigins); err != nil {
			return err
		}
		if len(l.AllowedOrigins) > 0 && l.Protocol != "http" && l.Protocol != "http3" && l.Protocol != "webtransport" && l.Protocol != "websocket" && l.Protocol != "mcp_http" && l.Protocol != "webrtc" {
			return errors.New("allowed origins require an HTTP, WebSocket, MCP HTTP, or WebRTC listener")
		}
		if l.Address == "" {
			if l.Protocol == "unix" || l.Protocol == "unixgram" || l.Protocol == "file_mailbox" || l.Protocol == "directory_mailbox" || l.Protocol == "append_log" {
				return errors.New("local listener requires an absolute path")
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
		if l.Protocol == "unixgram" && !unixSocketPath(l.Address) {
			return errors.New("unixgram listener requires a bounded absolute normalized socket path")
		}
		if l.Enabled && (l.Protocol == "unix" || l.Protocol == "unixgram") && !UnixAvailable() {
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
		if l.Protocol != "unix" && l.Protocol != "unixgram" && l.Protocol != "stdio" && l.Protocol != "file_mailbox" && l.Protocol != "directory_mailbox" && l.Protocol != "append_log" {
			var err error
			if l.Protocol == "udp" || l.Protocol == "coap_udp" || l.Protocol == "coap_tcp" || l.Protocol == "coap_oscore" {
				err = udpListenEndpoint(l.Address)
			} else {
				err = endpoint(l.Address, l.TLSKeyFile != "")
			}
			if err != nil {
				return err
			}
		}
		if err := validateFileMailbox(*l); err != nil {
			return err
		}
		if err := c.validateAppendLog(*l); err != nil {
			return err
		}
		if err := c.validateDirectoryMailbox(*l); err != nil {
			return err
		}
		if err := c.validateOSCORE(*l); err != nil {
			return err
		}
		if l.Protocol != "webrtc" && l.WebRTC != nil {
			return errors.New("WebRTC options require a WebRTC listener")
		}
		if l.Protocol == "webrtc" {
			if l.TLSCertFile == "" {
				return errors.New("webrtc listener requires TLS certificates")
			}
			if err := validateWebRTC(l.WebRTC); err != nil {
				return err
			}
		}
		if l.Enabled {
			if l.Protocol == "udp" || l.Protocol == "coap_udp" {
				udpListeners[l.ID] = l.Protocol
			}
			if l.Protocol == "coap_tcp" {
				coapTCPListeners[l.ID] = true
			}
			enabled++
			if l.Protocol == "file_mailbox" {
				fileMailboxEnabled++
			}
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
			if l.Protocol != "http" && l.Protocol != "http3" && l.Protocol != "webtransport" && l.Protocol != "rawquic" && l.Protocol != "rawquic_datagrams" && l.Protocol != "dtls" && l.Protocol != "coaps" && l.Protocol != "coaps_tcp" && l.Protocol != "udp" && l.Protocol != "coap_udp" && l.Protocol != "coap_tcp" && l.Protocol != "coap_oscore" && l.Protocol != "grpc" && l.Protocol != "framed" && l.Protocol != "websocket" && l.Protocol != "mcp_http" && l.Protocol != "webrtc" && l.Protocol != "unix" && l.Protocol != "unixgram" && l.Protocol != "stdio" && l.Protocol != "file_mailbox" && l.Protocol != "directory_mailbox" && l.Protocol != "append_log" {
				return fmt.Errorf("listener %s: protocol %s is not implemented", l.ID, l.Protocol)
			}
			if (l.Protocol == "unix" || l.Protocol == "unixgram") && l.TLSCertFile != "" {
				return errors.New("unix listener cannot use TLS certificates")
			}
			if (l.Protocol == "grpc" || l.Protocol == "http3" || l.Protocol == "webtransport" || l.Protocol == "rawquic" || l.Protocol == "rawquic_datagrams" || l.Protocol == "dtls" || l.Protocol == "coaps" || l.Protocol == "coaps_tcp" || l.Protocol == "webrtc") && l.TLSCertFile == "" {
				return errors.New(l.Protocol + " listener requires TLS certificates")
			}
			if l.Protocol == "udp" || l.Protocol == "coap_udp" || l.Protocol == "coap_tcp" {
				if l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) > 0 {
					return errors.New("udp listener cannot use TLS or origins")
				}
			}
			for _, op := range l.Operations {
				if op.IsWatch() && (c.Limits.Watches < 1 || c.Limits.WatchesPerPrincipal < 1) {
					return errors.New("enabled watch requires nonzero global and per-principal watch limits")
				}
				if op != core.Send && op != core.Reply && op != core.ReactionAdd && op != core.ContextRead && op != core.PinsRead && op != core.RoomRead && op != core.WaitingRead && op != core.PollResults && op != core.TopicRead && op != core.MembersRead && op != core.ChannelsRead && op != core.AgentsRead && op != core.ReceiptGet && op != core.HistoryRead && op != core.Search && !op.IsWatch() && op != core.Directory && op != core.ProfileUpdate && op != core.PresenceSet && op != core.TopicSet && op != core.RoomUpdate && op != core.PinsAdd && op != core.PinsRemove && op != core.ActivitySet && op != core.ReplySignalSet && op != core.ReplySignalCancel && op != core.PollCreate && op != core.PollVote && op != core.PollClose && op != core.MessageAction && op != core.MessageCorrect && op != core.MessageRetract {
					return fmt.Errorf("listener %s: operation %s is not implemented", l.ID, op)
				}
				if (op == core.HistoryRead || op == core.Search || op.IsWatch()) && c.CursorKeyFile == "" {
					return errors.New("enabled history/search/watch requires a cursor key file")
				}
				if (op == core.HistoryRead || op == core.Search || op.IsWatch() || op == core.Directory || op == core.ContextRead || op == core.PinsRead || op == core.RoomRead || op == core.WaitingRead || op == core.PollResults || op == core.TopicRead || op == core.MembersRead || op == core.ChannelsRead || op == core.AgentsRead) && (c.Limits.Sessions < 2 || c.Limits.SessionsPerPrincipal < 2) {
					return errors.New("enabled history/search/watch/directory/context/pins/room/waiting/poll/topic/members/channels/agents requires at least two global and per-principal sessions")
				}
			}
			if l.Protocol == "webrtc" && (c.Limits.Sessions < 2 || c.Limits.SessionsPerPrincipal < 2) {
				return errors.New("enabled WebRTC requires at least two global and per-principal sessions")
			}
		}
		if l.Protocol == "udp" || l.Protocol == "coap_udp" {
			udpProtocols[l.ID] = l.Protocol
		}
		if l.Protocol == "coap_tcp" {
			coapTCPProtocols[l.ID] = true
		}
		if l.Protocol == "unixgram" {
			unixgramProtocols[l.ID] = true
			if l.Enabled {
				unixgramListeners[l.ID] = true
			}
		}
	}
	if fileMailboxEnabled > 0 && enabled != 1 {
		return errors.New("file mailbox requires exactly one enabled listener")
	}
	if stdioEnabled > 0 && enabled != 1 {
		return errors.New("stdio requires exactly one enabled listener")
	}
	if (len(udpListeners) > 0 || len(unixgramListeners) > 0 || len(coapTCPListeners) > 0) && c.ResponseSigningKey == nil {
		return errors.New("enabled datagram listener requires response signing key")
	}
	if (len(udpListeners) > 0 || len(unixgramListeners) > 0 || len(coapTCPListeners) > 0) && len(principals) == 0 {
		return errors.New("enabled datagram listener requires a principal and return route")
	}
	routes := map[string]bool{}
	for _, r := range c.ReturnRoutes {
		if !id(r.ID) || routes[r.ID] || !principals[r.Principal] || r.Protocol == "" || r.Destination == "" || len(r.Destination) > 2048 || strings.ContainsAny(r.Destination, "\r\n\x00") {
			return errors.New("invalid return route")
		}
		if r.Protocol == "udp" || r.Protocol == "coap_udp" {
			if udpProtocols[r.Listener] != r.Protocol || !id(r.RecipientKeyID) || !absolute(r.RecipientPublicKeyFile) || (r.Listener == "" || r.RecipientKeyID == "" || r.RecipientPublicKeyFile == "") {
				return errors.New("invalid UDP return route")
			}
			if err := udpDestination(r.Destination); err != nil {
				return fmt.Errorf("invalid UDP return route: %w", err)
			}
		} else if r.Protocol == "coap_tcp" {
			if !coapTCPProtocols[r.Listener] || r.Destination != "connection" || !id(r.RecipientKeyID) || !absolute(r.RecipientPublicKeyFile) {
				return errors.New("invalid CoAP TCP connection return route")
			}
		} else if r.Protocol == "unixgram" {
			if !unixgramProtocols[r.Listener] || !id(r.RecipientKeyID) || !absolute(r.RecipientPublicKeyFile) || r.Listener == "" {
				return errors.New("invalid unixgram return route")
			}
			if !unixSocketPath(r.Destination) {
				return errors.New("invalid unixgram return route path")
			}
		} else if r.Listener != "" || r.RecipientKeyID != "" || r.RecipientPublicKeyFile != "" {
			return errors.New("return route datagram fields require a datagram protocol")
		}
		routes[r.ID] = true
	}
	for listener, protocol := range udpListeners {
		for principal := range principals {
			count := 0
			for _, route := range c.ReturnRoutes {
				if route.Protocol == protocol && route.Listener == listener && route.Principal == principal {
					count++
				}
			}
			if count != 1 {
				return fmt.Errorf("UDP listener %s requires exactly one route for principal %s", listener, principal)
			}
		}
	}
	for listener := range coapTCPListeners {
		for principal := range principals {
			count := 0
			for _, route := range c.ReturnRoutes {
				if route.Protocol == "coap_tcp" && route.Listener == listener && route.Principal == principal {
					count++
				}
			}
			if count != 1 {
				return fmt.Errorf("CoAP TCP listener %s requires exactly one route for principal %s", listener, principal)
			}
		}
	}

	for listener := range unixgramListeners {
		for principal := range principals {
			count := 0
			for _, route := range c.ReturnRoutes {
				if route.Protocol == "unixgram" && route.Listener == listener && route.Principal == principal {
					count++
				}
			}
			if count != 1 {
				return fmt.Errorf("unixgram listener %s requires exactly one route for principal %s", listener, principal)
			}
		}
	}
	return c.validateLimits()
}

func unixSocketPath(path string) bool {
	if !absolute(path) || filepath.Clean(path) != path {
		return false
	}
	// sockaddr_un.sun_path is 104 bytes on Darwin and 108 on Linux,
	// including the terminating NUL.
	limit := 107
	if runtime.GOOS == "darwin" {
		limit = 103
	}
	return len(path) <= limit
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

func udpListenEndpoint(address string) error {
	host, port, err := net.SplitHostPort(address)
	p, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || p < 0 || p > 65535 {
		return errors.New("invalid UDP listener endpoint")
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsMulticast() {
		return errors.New("UDP listener requires a numeric address")
	}
	return nil
}

func udpDestination(address string) error {
	host, port, err := net.SplitHostPort(address)
	p, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || p < 1 || p > 65535 {
		return errors.New("UDP destination requires a nonzero port")
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) {
		return errors.New("UDP destination requires a numeric unicast address")
	}
	return nil
}
