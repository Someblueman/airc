package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
)

// Kind is how a carrier moves requests, and so who owns it.
type Kind uint8

const (
	// Stream carriers accept connections (TCP, TLS, Unix stream and
	// seqpacket, QUIC, HTTP); serve owns them.
	Stream Kind = iota + 1
	// Packet carriers take whole messages (datagrams, polled mailboxes);
	// serve owns them.
	Packet
	// Process carriers serve one parent-owned stream or mailbox and run
	// only under their own command, as the sole enabled listener.
	Process
)

// AddressShape is what a listener's address names.
type AddressShape uint8

const (
	// HostPort is a numeric IP:port; an empty address means 127.0.0.1:0.
	HostPort AddressShape = iota + 1
	// DatagramPort is a numeric, non-multicast IP:port.
	DatagramPort
	// Path is an absolute filesystem path.
	Path
	// NoAddress: the parent process supplies the requests.
	NoAddress
)

// TLSPolicy is whether a listener may, must or must not use TLS.
type TLSPolicy uint8

const (
	// TLSOptional carriers have a plaintext form: TLS is allowed, and
	// plaintext is allowed on any address, as on IRC.
	TLSOptional TLSPolicy = iota + 1
	// TLSRequired carriers are encrypted by definition.
	TLSRequired
	// TLSForbidden carriers have no TLS form.
	TLSForbidden
)

// Authors is who a carrier's requests post as.
type Authors uint8

const (
	// RequestAuthors: each plain contract request names its author, and the
	// listener takes the author access policy. On local carriers the access
	// gate is the kernel or owner-only boundary they already have (socket
	// and file permissions, peer credentials, the parent process); AIRC's
	// per-source limits see listener:<id>.
	RequestAuthors Authors = iota + 1
	// NativeAuthors: the carrier tunnels AIRC's own protocol to the upstream,
	// byte for byte, after asserting the client's source with WEBIRC. AIRC
	// itself admits the client (its PASS, nickname and account), so the
	// listener has no author policy, only connection limits.
	NativeAuthors
)

// soleGroup is a family of Process carriers that must be the only enabled
// listener, in the order Validate reports them.
type soleGroup uint8

const (
	fileMailboxOwner soleGroup = iota + 1
	parentStream
)

// soleMessages refuse an exclusive carrier beside other enabled listeners.
var soleMessages = [...]string{
	fileMailboxOwner: "file mailbox requires exactly one enabled listener",
	parentStream:     "parent-owned stream requires exactly one enabled listener",
}

// Transport describes one carrier. Every protocol is defined once, in
// transports; configuration validation, the service, the dispatcher's author
// policy, the capabilities report and the install command read it. A
// carrier's constructor is its entry in the service's owner table, keyed by
// Name. Operation limits (send-only carriers, MQTT's mutations) stay in each
// carrier's validator, where they share one error message with its options.
type Transport struct {
	Name    string
	Kind    Kind
	Network string // the socket or IPC family; stream carriers listen on it
	Address AddressShape
	TLS     TLSPolicy
	// Origins: browser origins may be allowlisted (allowed_origins).
	Origins bool
	Authors Authors
	// BoundAuthor: the listener may configure bound_author, for one-shot
	// producers that would otherwise keep tokens at rest.
	BoundAuthor bool
	// Platforms are GOOS or GOOS/GOARCH values the carrier runs on; none
	// means every platform. Configuration accepts a disabled listener
	// anywhere; an enabled one only here.
	Platforms []string
	// Command is the airc-gateway command that serves a Process carrier.
	Command string

	title  string    // a Process carrier's name when serve refuses it
	sole   soleGroup // a Process carrier's exclusivity group
	blocks []block   // the listener sub-blocks it owns (listenerBlocks)
	// defaults fills in options a listener may omit.
	defaults func(*Listener)
	// directories are the private directories a listener's address must
	// hold, relative to it.
	directories []string
	// checkAddress validates a Unix socket Path.
	checkAddress func(l Listener) error
}

var unixLike = []string{"darwin", "linux"}

// transports is the registry, in documentation order: every listener
// protocol.
var transports = []Transport{
	{Name: "http", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Origins: true, Authors: RequestAuthors},
	{Name: "websocket", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Origins: true, Authors: RequestAuthors},
	{Name: "mcp_http", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Origins: true, Authors: RequestAuthors},
	{Name: "grpc", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Authors: RequestAuthors},
	{Name: "http3", Kind: Stream, Network: "udp", Address: HostPort, TLS: TLSRequired, Origins: true, Authors: RequestAuthors},
	{Name: "webtransport", Kind: Stream, Network: "udp", Address: HostPort, TLS: TLSRequired, Origins: true, Authors: RequestAuthors},
	{Name: "framed", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Authors: RequestAuthors},
	{Name: "irc_websocket", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Origins: true, Authors: NativeAuthors},
	{Name: "irc_connect", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Authors: NativeAuthors},
	{Name: "unix", Kind: Stream, Network: "unix", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, checkAddress: unixStreamPath},
	{Name: "unixpacket", Kind: Stream, Network: "unixpacket", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: []string{"linux"}, checkAddress: unixPacketPath},
	{Name: "unixgram", Kind: Packet, Network: "unixgram", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, checkAddress: unixgramPath},
	{Name: "stdio", Kind: Process, Network: "pipe", Address: NoAddress, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, Command: "serve-stdio", title: "stdio listener", sole: parentStream},
	{Name: "socketpair", Kind: Process, Network: "unix", Address: NoAddress, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, Command: "serve-socketpair", title: "socketpair listener", sole: parentStream},
	{Name: "inherited", Kind: Process, Network: "unix", Address: NoAddress, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, Command: "serve-inherited", title: "inherited listener", sole: parentStream},
	{Name: "udp", Kind: Packet, Network: "udp", Address: DatagramPort, TLS: TLSForbidden, Authors: RequestAuthors},
	{Name: "rawquic", Kind: Stream, Network: "udp", Address: HostPort, TLS: TLSRequired, Authors: RequestAuthors},
	{Name: "rawquic_datagrams", Kind: Stream, Network: "udp", Address: HostPort, TLS: TLSRequired, Authors: RequestAuthors},
	{Name: "dtls", Kind: Packet, Network: "udp", Address: HostPort, TLS: TLSRequired, Authors: RequestAuthors},
	{Name: "coap_udp", Kind: Packet, Network: "udp", Address: DatagramPort, TLS: TLSForbidden, Authors: RequestAuthors},
	{Name: "coaps", Kind: Packet, Network: "udp", Address: HostPort, TLS: TLSRequired, Authors: RequestAuthors},
	{Name: "zeromq", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSForbidden, Authors: RequestAuthors, blocks: []block{localREPBlock}},
	{Name: "nng", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSForbidden, Authors: RequestAuthors, blocks: []block{localREPBlock}},
	{Name: "mqtt", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Authors: RequestAuthors, blocks: []block{mqttBlock}, defaults: mqttDefaults},
	{Name: "mqtt_websocket", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Origins: true, Authors: RequestAuthors, blocks: []block{mqttBlock}, defaults: mqttDefaults},
	{Name: "file_mailbox", Kind: Process, Network: "file", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: unixLike, Command: "mailbox-once", title: "file mailbox", sole: fileMailboxOwner, blocks: []block{fileMailboxBlock}},
	{Name: "directory_mailbox", Kind: Packet, Network: "file", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: unixLike, blocks: []block{fileMailboxBlock}, directories: []string{"", "inbox", "processing", "results"}},
	{Name: "append_log", Kind: Packet, Network: "file", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: unixLike, blocks: []block{fileMailboxBlock}, directories: []string{"", "logs"}},
}

// listenerTransports indexes the listener protocols.
var listenerTransports = func() map[string]Transport {
	index := make(map[string]Transport, len(transports))
	for _, t := range transports {
		index[t.Name] = t
	}
	return index
}()

// Transports returns the registry in documentation order.
func Transports() []Transport { return slices.Clone(transports) }

// Lookup returns a listener protocol's descriptor.
func Lookup(protocol string) (Transport, bool) {
	t, ok := listenerTransports[protocol]
	return t, ok
}

// unknownTransport is how Validate treats a protocol with no descriptor:
// accepted only while disabled, and checked as an encrypted TCP endpoint.
func unknownTransport(protocol string) Transport {
	return Transport{Name: protocol, Address: HostPort, TLS: TLSRequired}
}

// Available reports whether the carrier runs on this platform.
func (t Transport) Available() bool { return availableOn(t.Platforms, runtime.GOOS, runtime.GOARCH) }

func availableOn(platforms []string, goos, goarch string) bool {
	return len(platforms) == 0 || slices.Contains(platforms, goos) || slices.Contains(platforms, goos+"/"+goarch)
}

// Available reports whether a known listener protocol runs on this
// platform.
func Available(protocol string) bool {
	t, ok := Lookup(protocol)
	return ok && t.Available()
}

// ServeRefusal is why serve will not run an enabled Process carrier.
func (t Transport) ServeRefusal() error { return errors.New(t.title + " requires " + t.Command) }

// AuthorCarrier reports listeners that accept per-request authors (plain
// contract requests naming who posts), and so take the author access policy.
func AuthorCarrier(protocol string) bool {
	t, _ := Lookup(protocol)
	return t.Authors == RequestAuthors
}

// NativeCarrier reports listeners that tunnel AIRC's own protocol to the
// upstream (IRC over WebSocket, IRC through HTTP CONNECT). They need the
// upstream, take no operations or author policy, and bound only their
// connections.
func NativeCarrier(protocol string) bool {
	t, _ := Lookup(protocol)
	return t.Authors == NativeAuthors
}

// plaintext reports a host:port listener that may serve plaintext off
// loopback, IRC style.
func (t Transport) plaintext() bool { return t.TLS != TLSRequired }

// unixSocket reports the Unix socket carriers.
func (t Transport) unixSocket() bool {
	return t.Address == Path && (t.Network == "unix" || t.Network == "unixpacket" || t.Network == "unixgram")
}

func unixStreamPath(l Listener) error {
	if !absolute(l.Address) || filepath.Clean(l.Address) != l.Address {
		return errors.New("unix listener requires an absolute normalized socket path")
	}
	return nil
}

func unixPacketPath(l Listener) error {
	if !absolute(l.Address) || filepath.Clean(l.Address) != l.Address || len(l.Address) > 107 {
		return errors.New("unixpacket listener requires a bounded absolute normalized socket path")
	}
	return nil
}

func unixgramPath(l Listener) error {
	if !unixSocketPath(l.Address) {
		return fmt.Errorf("%s listener requires a bounded absolute normalized socket path", l.Protocol)
	}
	return nil
}
