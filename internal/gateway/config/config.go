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

type Config struct {
	Version        int              `json:"version"`
	Realm          string           `json:"realm"`
	StateDir       string           `json:"state_dir"`
	Upstream       *Upstream        `json:"upstream,omitempty"`
	Carriers       []string         `json:"carriers,omitempty"`
	Listeners      []Listener       `json:"listeners"`
	Limits         Limits           `json:"limits"`
	Retry          Retry            `json:"retry"`
	CursorKeyFile  string           `json:"cursor_key_file,omitempty"`
	RedisPubSub    []RedisPubSub    `json:"redis_pubsub,omitempty"`
	RedisStreams   []RedisStreams   `json:"redis_streams,omitempty"`
	IRCBridges     []IRCBridge      `json:"irc_bridges,omitempty"`
	XMPPBridges    []XMPPBridge     `json:"xmpp_bridges,omitempty"`
	SFTP           []SFTP           `json:"sftp,omitempty"`
	PostgresNotify []PostgresNotify `json:"postgres_notify,omitempty"`
}

// Upstream is the AIRC endpoint every per-request author connects through.
// GatewayTokenFile, when set, holds aircd's --gateway-token-file credential:
// each author session then asserts its client's real source with WEBIRC, so
// aircd applies its per-address limits to that client.
type Upstream struct {
	Network          string `json:"network"`
	Address          string `json:"address"`
	AccessTokenFile  string `json:"access_token_file,omitempty"`
	GatewayTokenFile string `json:"gateway_token_file,omitempty"`
	TLSServerName    string `json:"tls_server_name,omitempty"`
	TLSCAFile        string `json:"tls_ca_file,omitempty"`
}

type Listener struct {
	UDPLite        *UDPLite         `json:"udp_lite,omitempty"`
	IPv4Broadcast  *IPv4Broadcast   `json:"ipv4_broadcast,omitempty"`
	IPv6Multicast  *IPv6Multicast   `json:"ipv6_multicast,omitempty"`
	ICMPv6         *ICMPv6          `json:"icmp_v6,omitempty"`
	IPv4Multicast  *IPv4Multicast   `json:"ipv4_multicast,omitempty"`
	SNMP           *SNMP            `json:"snmp,omitempty"`
	SignedSyslog   *SignedSyslog    `json:"signed_syslog,omitempty"`
	Syslog         *Syslog          `json:"syslog,omitempty"`
	STOMP          *STOMP           `json:"stomp,omitempty"`
	AMQP           *AMQP            `json:"amqp,omitempty"`
	MQTTSN         *MQTTSN          `json:"mqtt_sn,omitempty"`
	MQTT           *MQTT            `json:"mqtt,omitempty"`
	BundleProtocol *BundleProtocol  `json:"bundle_protocol,omitempty"`
	POSIXQueue     *POSIXQueue      `json:"posix_queue,omitempty"`
	SharedMemory   *SharedMemory    `json:"shared_memory,omitempty"`
	MachIPC        *MachIPC         `json:"mach_ipc,omitempty"`
	AllowedOrigins []string         `json:"allowed_origins,omitempty"`
	ID             string           `json:"id"`
	Protocol       string           `json:"protocol"`
	Address        string           `json:"address"`
	Enabled        bool             `json:"enabled"`
	TLSCertFile    string           `json:"tls_cert_file,omitempty"`
	TLSKeyFile     string           `json:"tls_key_file,omitempty"`
	Operations     []core.Operation `json:"operations"`
	WebRTC         *WebRTC          `json:"webrtc,omitempty"`
	OSCORE         *OSCORE          `json:"oscore,omitempty"`
	SCPInbox       *SCPInbox        `json:"scp_inbox,omitempty"`
	// Author access policy (see ListenerAccess). Operations above also bound
	// author requests; when empty, authors get every available operation.
	Targets      []core.Target   `json:"targets,omitempty"`
	Guests       *bool           `json:"guests,omitempty"`
	PasswordFile string          `json:"password_file,omitempty"`
	Limits       *ListenerLimits `json:"limits,omitempty"`
	// Local author carriers (see local_author.go).
	BoundAuthor *BoundAuthor `json:"bound_author,omitempty"`
	ReplyTo     string       `json:"reply_to,omitempty"`
}

type Limits struct {
	Workers int `json:"workers"`
	// Connections caps open client connections across every listener;
	// Sessions caps the upstream AIRC sessions their requests use.
	Connections       int `json:"connections"`
	Sessions          int `json:"sessions"`
	SessionsPerAuthor int `json:"sessions_per_author"`
	// IdleSessions caps the upstream sessions kept, between requests, for
	// their author's next request; 0 dials a session for every request.
	IdleSessions     int   `json:"idle_sessions"`
	Watches          int   `json:"watches"`
	WatchesPerAuthor int   `json:"watches_per_author"`
	Pending          int   `json:"pending"`
	PendingPerAuthor int   `json:"pending_per_author"`
	JournalBytes     int64 `json:"journal_bytes"`
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
	return Config{Listeners: []Listener{}, Limits: Limits{Workers: 8, Connections: 1024, Sessions: 32, SessionsPerAuthor: 4, IdleSessions: 128, Watches: 8, WatchesPerAuthor: 2, Pending: 4096, PendingPerAuthor: 128, JournalBytes: 256 << 20}, Retry: Retry{Policy: core.Retry, SendAttempts: 100, LookupAttempts: 100, LifetimeSeconds: 86400, BackoffSeconds: 1, MaxBackoffSeconds: 60, TerminalRetentionSeconds: 7 * 86400}}
}

func Parse(data []byte) (Config, error) {
	if err := RemovedFields(data); err != nil {
		return Config{}, err
	}
	c := Defaults()
	if err := jsoninput.DecodeDepth(data, &c, MaxBytes, 10); err != nil {
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
	if err := c.validateUpstream(); err != nil {
		return err
	}
	if err := c.validateCarriers(); err != nil {
		return err
	}
	if len(c.Listeners) > 128 {
		return errors.New("configuration count limit exceeded")
	}
	// Bridges, then the carriers whose listeners constrain each other
	// (shared keys, endpoints, directories and platform support).
	for _, check := range []func(Config) error{
		Config.validateRedisPubSub,
		Config.validateRedisStreams,
		Config.validateIRCBridges,
		Config.validateXMPPBridges,
		Config.validateBridgeNamespaces,
		Config.validatePostgresNotify,
		Config.validateSFTP,
		Config.validateSystemVQueues,
		Config.validateUDPLite,
		Config.validateIPv4Broadcast,
		Config.validateIPv6Multicast,
		Config.validateICMPv6,
		Config.validateIPv4Multicast,
		Config.validatePOSIXQueues,
		Config.validateSharedMemory,
		Config.validateMappedFile,
		Config.validateSCTP,
		Config.validateMachIPC,
		Config.validateSCPInbox,
	} {
		if err := check(*c); err != nil {
			return err
		}
	}
	listeners, addresses := map[string]bool{}, map[string]bool{}
	var sole [len(soleMessages)]int
	enabled := 0
	for i := range c.Listeners {
		l := &c.Listeners[i]
		if !id(l.ID) || listeners[l.ID] || l.Protocol == "" {
			return errors.New("invalid or duplicate listener")
		}
		listeners[l.ID] = true
		t, known := Lookup(l.Protocol)
		if !known {
			t = unknownTransport(l.Protocol)
		}
		if t.defaults != nil {
			t.defaults(l)
		}
		if err := ValidateOrigins(l.AllowedOrigins); err != nil {
			return err
		}
		if len(l.AllowedOrigins) > 0 && !t.Origins {
			return errors.New("allowed origins require an HTTP, WebSocket, MCP HTTP, or WebRTC listener")
		}
		if l.Address == "" {
			switch t.Address {
			case SCTPPort:
				return errors.New("SCTP listener requires an explicit IP:port")
			case ServiceName:
				return fmt.Errorf("%s listener requires a bootstrap service name", l.Protocol)
			case ICMPEndpoint:
				return fmt.Errorf("%s listener requires an explicit [IPv6]:channel endpoint", l.Protocol)
			case Path:
				return errors.New("local listener requires an absolute path")
			case NoAddress:
			default:
				l.Address = "127.0.0.1:0"
			}
		}
		if t.Address == NoAddress && (l.Address != "" || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) > 0) {
			return fmt.Errorf("%s listener cannot use address, TLS, or origins", l.Protocol)
		}
		if t.Address == Path && t.checkAddress != nil {
			if err := t.checkAddress(*l); err != nil {
				return err
			}
		}
		if l.Enabled && t.unixSocket() && !Available("unix") {
			return errors.New("unix listener is unavailable on this platform")
		}
		if l.Enabled && t.unixSocket() && !t.Available() {
			return fmt.Errorf("%s listener is unavailable on this platform", l.Protocol)
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
		var err error
		switch t.Address {
		case GroupPort:
			err = t.checkAddress(*l)
		case DatagramPort:
			err = udpListenEndpoint(l.Address)
		case HostPort:
			// IRC-style access: a protocol with a plaintext form needs no
			// TLS, even off loopback; TLS stays an option.
			err = endpoint(l.Address, l.TLSKeyFile != "" || t.plaintext())
		}
		if err != nil {
			return err
		}
		if err := validateListenerAccess(l); err != nil {
			return fmt.Errorf("listener %s: %w", l.ID, err)
		}
		if err := c.validateLocalAuthor(t, *l); err != nil {
			return fmt.Errorf("listener %s: %w", l.ID, err)
		}
		if err := c.validateBlocks(t, *l); err != nil {
			return err
		}
		if l.Enabled {
			enabled++
			if t.sole != 0 {
				if t.sole == parentStream && !t.Available() {
					return fmt.Errorf("%s listener is unavailable on this platform", l.Protocol)
				}
				sole[t.sole]++
			}
			// A UDP listener (HTTP/3, say) may share a TCP listener's
			// port; two on the same family may not.
			key := l.Address
			if t.Network == "udp" {
				key = "udp " + l.Address
			}
			if addresses[key] {
				return errors.New("duplicate enabled listener address")
			}
			addresses[key] = true
			if !known {
				return fmt.Errorf("listener %s: protocol %s is not implemented", l.ID, l.Protocol)
			}
			if t.unixSocket() && l.TLSCertFile != "" {
				return errors.New("unix listener cannot use TLS certificates")
			}
			if t.TLS == TLSRequired && l.TLSCertFile == "" {
				return errors.New(l.Protocol + " listener requires TLS certificates")
			}
			if t.Address == DatagramPort && t.TLS == TLSForbidden && (l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) > 0) {
				return errors.New("udp listener cannot use TLS or origins")
			}
			for _, op := range l.Operations {
				if op.IsWatch() && (c.Limits.Watches < 1 || c.Limits.WatchesPerAuthor < 1) {
					return errors.New("enabled watch requires nonzero global and per-author watch limits")
				}
				if op != core.Send && op != core.Reply && op != core.ReactionAdd && op != core.ContextRead && op != core.PinsRead && op != core.RoomRead && op != core.WaitingRead && op != core.PollResults && op != core.TopicRead && op != core.MembersRead && op != core.ChannelsRead && op != core.AgentsRead && op != core.ReceiptGet && op != core.HistoryRead && op != core.Search && !op.IsWatch() && op != core.Directory && op != core.ProfileUpdate && op != core.PresenceSet && op != core.TopicSet && op != core.RoomUpdate && op != core.PinsAdd && op != core.PinsRemove && op != core.ActivitySet && op != core.ReplySignalSet && op != core.ReplySignalCancel && op != core.PollCreate && op != core.PollVote && op != core.PollClose && op != core.MessageAction && op != core.MessageCorrect && op != core.MessageRetract && op != core.AccountRegister {
					return fmt.Errorf("listener %s: operation %s is not implemented", l.ID, op)
				}
				if (op == core.HistoryRead || op == core.Search || op.IsWatch()) && c.CursorKeyFile == "" {
					return errors.New("enabled history/search/watch requires a cursor key file")
				}
				if (op == core.HistoryRead || op == core.Search || op.IsWatch() || op == core.Directory || op == core.ContextRead || op == core.PinsRead || op == core.RoomRead || op == core.WaitingRead || op == core.PollResults || op == core.TopicRead || op == core.MembersRead || op == core.ChannelsRead || op == core.AgentsRead) && (c.Limits.Sessions < 2 || c.Limits.SessionsPerAuthor < 2) {
					return errors.New("enabled history/search/watch/directory/context/pins/room/waiting/poll/topic/members/channels/agents requires at least two global and per-author sessions")
				}
			}
			if l.Protocol == "webrtc" && (c.Limits.Sessions < 2 || c.Limits.SessionsPerAuthor < 2) {
				return errors.New("enabled WebRTC requires at least two global and per-author sessions")
			}
		}
	}
	for group, message := range soleMessages {
		if sole[group] > 0 && enabled != 1 {
			return errors.New(message)
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
