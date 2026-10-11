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
	// seqpacket, SCTP, QUIC, HTTP); serve owns them.
	Stream Kind = iota + 1
	// Packet carriers take whole messages (datagrams, queues, shared memory,
	// polled mailboxes); serve owns them.
	Packet
	// Process carriers serve one parent-owned stream, mailbox or import and
	// run only under their own command, as the sole enabled listener.
	Process
	// Bridge carriers are top-level sections: the gateway is the client of
	// another service. They have no listener protocol.
	Bridge
)

// AddressShape is what a listener's address names.
type AddressShape uint8

const (
	// HostPort is a numeric IP:port; an empty address means 127.0.0.1:0.
	HostPort AddressShape = iota + 1
	// DatagramPort is a numeric, non-multicast IP:port.
	DatagramPort
	// GroupPort is a multicast, broadcast or UDP-Lite IP:port, checked by
	// the protocol's own options.
	GroupPort
	// SCTPPort is an explicit literal IP:port.
	SCTPPort
	// ICMPEndpoint is an explicit [IPv6]:channel.
	ICMPEndpoint
	// ServiceName is an explicit Mach bootstrap service name.
	ServiceName
	// QueueName is a queue key or shared-memory name, checked by the
	// protocol's own validator.
	QueueName
	// Path is an absolute filesystem path.
	Path
	// NoAddress: the parent process or the importer supplies the requests.
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
	// listener takes the author access policy. On local and offline carriers
	// the access gate is the kernel or owner-only boundary they already have
	// (socket and file permissions, peer credentials, the parent process, the
	// operator who runs an import); AIRC's per-source limits see listener:<id>.
	RequestAuthors Authors = iota + 1
	// RouteAuthors: the provider protocol verifies the sender, and requests
	// post as the route's configured or provider-derived author.
	RouteAuthors
)

// soleGroup is a family of Process carriers that must be the only enabled
// listener, in the order Validate reports them.
type soleGroup uint8

const (
	offlineImport soleGroup = iota + 1
	fileMailboxOwner
	parentStream
)

// soleMessages refuse an exclusive carrier beside other enabled listeners.
var soleMessages = [...]string{
	offlineImport:    "offline bundle requires exactly one enabled listener",
	fileMailboxOwner: "file mailbox requires exactly one enabled listener",
	parentStream:     "parent-owned stream requires exactly one enabled listener",
}

// Transport describes one carrier. Every protocol is defined once, in
// transports; configuration validation, the service, discovery and the
// install command read it. A carrier's constructor is its entry in the
// service's owner table, keyed by Name.
type Transport struct {
	Name    string
	Kind    Kind
	Network string // the socket or IPC family, for documentation and Unix checks
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
	// Section is a Bridge's top-level configuration key.
	Section string

	title  string    // a Process carrier's name when serve refuses it
	sole   soleGroup // a Process carrier's exclusivity group
	blocks []block   // the listener sub-blocks it owns (listenerBlocks)
	// defaults fills in options a listener may omit.
	defaults func(*Listener)
	// replyTo, when set, makes reply_to required and checks its value.
	replyTo func(c Config, l Listener) bool
	// directories are the private directories a listener's address must
	// hold, relative to it.
	directories []string
	// checkAddress validates a GroupPort address with the listener's options,
	// or a Unix socket Path.
	checkAddress func(l Listener) error
}

var (
	unixLike = []string{"darwin", "linux"}
	arm64    = []string{"darwin/arm64", "linux/arm64"}
)

// transports is the registry, in documentation order: every listener
// protocol, then every bridge in the order the service starts them.
var transports = []Transport{
	{Name: "http", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Origins: true, Authors: RequestAuthors},
	{Name: "websocket", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Origins: true, Authors: RequestAuthors},
	{Name: "mcp_http", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Origins: true, Authors: RequestAuthors},
	{Name: "grpc", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Authors: RequestAuthors},
	{Name: "http3", Kind: Stream, Network: "udp", Address: HostPort, TLS: TLSRequired, Origins: true, Authors: RequestAuthors},
	{Name: "webtransport", Kind: Stream, Network: "udp", Address: HostPort, TLS: TLSRequired, Origins: true, Authors: RequestAuthors},
	{Name: "framed", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Authors: RequestAuthors},
	{Name: "unix", Kind: Stream, Network: "unix", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, checkAddress: unixStreamPath},
	{Name: "unixpacket", Kind: Stream, Network: "unixpacket", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: []string{"linux"}, checkAddress: unixPacketPath},
	{Name: "unixgram", Kind: Packet, Network: "unixgram", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, checkAddress: unixgramPath},
	{Name: "stdio", Kind: Process, Network: "pipe", Address: NoAddress, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, Command: "serve-stdio", title: "stdio listener", sole: parentStream},
	{Name: "socketpair", Kind: Process, Network: "unix", Address: NoAddress, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, Command: "serve-socketpair", title: "socketpair listener", sole: parentStream},
	{Name: "inherited", Kind: Process, Network: "unix", Address: NoAddress, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, Command: "serve-inherited", title: "inherited listener", sole: parentStream},
	{Name: "fifo", Kind: Process, Network: "fifo", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, Command: "serve-fifo", title: "fifo listener", sole: parentStream, blocks: []block{fifoBlock}, directories: []string{""}},
	{Name: "uart", Kind: Process, Network: "serial", Address: Path, TLS: TLSOptional, Authors: RequestAuthors, Platforms: unixLike, Command: "serve-uart", title: "uart listener", sole: parentStream, blocks: []block{uartBlock}},
	{Name: "system_v_queue", Kind: Packet, Network: "sysvmsg", Address: QueueName, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: []string{"darwin/arm64", "linux/amd64", "linux/arm64"}, replyTo: systemVQueueReply},
	{Name: "posix_mqueue", Kind: Packet, Network: "mqueue", Address: QueueName, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: []string{"linux/amd64", "linux/arm64"}, replyTo: posixQueueReply},
	{Name: "shared_memory", Kind: Packet, Network: "shm", Address: QueueName, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: arm64, replyTo: sharedMemoryReply},
	{Name: "mapped_file", Kind: Packet, Network: "file", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: arm64, replyTo: mappedFileReply},
	{Name: "mach_ipc", Kind: Packet, Network: "mach", Address: ServiceName, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: []string{"darwin/arm64"}},
	{Name: "udp", Kind: Packet, Network: "udp", Address: DatagramPort, TLS: TLSForbidden, Authors: RequestAuthors},
	{Name: "udp_lite", Kind: Packet, Network: "udplite", Address: GroupPort, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: []string{"linux"}, checkAddress: func(l Listener) error { return l.UDPLite.Validate(l.Address) }},
	{Name: "ipv4_multicast", Kind: Packet, Network: "udp4", Address: GroupPort, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, checkAddress: func(l Listener) error { return l.IPv4Multicast.Validate(l.Address) }},
	{Name: "ipv6_multicast", Kind: Packet, Network: "udp6", Address: GroupPort, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, checkAddress: func(l Listener) error { return l.IPv6Multicast.Validate(l.Address) }},
	{Name: "ipv4_broadcast", Kind: Packet, Network: "udp4", Address: GroupPort, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, checkAddress: func(l Listener) error { return l.IPv4Broadcast.Validate(l.Address) }},
	{Name: "icmp_v6", Kind: Packet, Network: "ip6:ipv6-icmp", Address: ICMPEndpoint, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: []string{"darwin/arm64"}},
	{Name: "sctp", Kind: Stream, Network: "sctp", Address: SCTPPort, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: []string{"linux/arm64"}},
	{Name: "sctp_unordered", Kind: Stream, Network: "sctp", Address: SCTPPort, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: []string{"linux/arm64"}},
	{Name: "sctp_pr_ttl", Kind: Stream, Network: "sctp", Address: SCTPPort, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: []string{"linux/arm64"}},
	{Name: "rawquic", Kind: Stream, Network: "udp", Address: HostPort, TLS: TLSRequired, Authors: RequestAuthors},
	{Name: "rawquic_datagrams", Kind: Stream, Network: "udp", Address: HostPort, TLS: TLSRequired, Authors: RequestAuthors},
	{Name: "dtls", Kind: Packet, Network: "udp", Address: HostPort, TLS: TLSRequired, Authors: RequestAuthors},
	{Name: "coap_udp", Kind: Packet, Network: "udp", Address: DatagramPort, TLS: TLSForbidden, Authors: RequestAuthors},
	{Name: "coap_tcp", Kind: Stream, Network: "tcp", Address: DatagramPort, TLS: TLSForbidden, Authors: RequestAuthors},
	{Name: "coaps", Kind: Packet, Network: "udp", Address: HostPort, TLS: TLSRequired, Authors: RequestAuthors},
	{Name: "coaps_tcp", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSRequired, Authors: RequestAuthors},
	{Name: "coap_oscore", Kind: Packet, Network: "udp", Address: DatagramPort, TLS: TLSForbidden, Authors: RequestAuthors, Platforms: unixLike, blocks: []block{oscoreBlock}},
	{Name: "webrtc", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSRequired, Origins: true, Authors: RequestAuthors, blocks: []block{webRTCBlock}},
	{Name: "zeromq", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSForbidden, Authors: RequestAuthors, blocks: []block{localREPBlock}},
	{Name: "nng", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSForbidden, Authors: RequestAuthors, blocks: []block{localREPBlock}},
	{Name: "mqtt", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Authors: RequestAuthors, blocks: []block{mqttBlock}, defaults: mqttDefaults},
	{Name: "mqtt_sn", Kind: Packet, Network: "udp", Address: DatagramPort, TLS: TLSForbidden, Authors: RequestAuthors, blocks: []block{mqttSNBlock}},
	{Name: "amqp", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Authors: RequestAuthors, blocks: []block{amqpBlock}, defaults: amqpDefaults},
	{Name: "stomp", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSOptional, Authors: RequestAuthors, blocks: []block{stompBlock}, defaults: stompDefaults},
	{Name: "file_mailbox", Kind: Process, Network: "file", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: unixLike, Command: "mailbox-once", title: "file mailbox", sole: fileMailboxOwner, blocks: []block{fileMailboxBlock}},
	{Name: "directory_mailbox", Kind: Packet, Network: "file", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: unixLike, blocks: []block{fileMailboxBlock}, directories: []string{"", "inbox", "processing", "results"}},
	{Name: "append_log", Kind: Packet, Network: "file", Address: Path, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: unixLike, blocks: []block{fileMailboxBlock}, directories: []string{"", "logs"}},
	{Name: "scp_inbox", Kind: Process, Network: "ssh", Address: NoAddress, TLS: TLSForbidden, Authors: RouteAuthors, Platforms: unixLike, Command: "scp-inbox", title: "SCP inbox", sole: parentStream},
	{Name: "offline_bundle", Kind: Process, Network: "file", Address: NoAddress, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: unixLike, Command: "import-bundle", title: "offline bundle", sole: offlineImport, blocks: []block{offlineBundleBlock}},
	{Name: "bpv7", Kind: Process, Network: "file", Address: NoAddress, TLS: TLSForbidden, Authors: RequestAuthors, BoundAuthor: true, Platforms: unixLike, Command: "import-bpv7", title: "BPv7", sole: offlineImport, blocks: []block{bundleProtocolBlock}},
	{Name: "syslog_tls", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSRequired, Authors: RouteAuthors, blocks: []block{syslogBlock}},
	{Name: "syslog_signed", Kind: Stream, Network: "tcp", Address: HostPort, TLS: TLSRequired, Authors: RouteAuthors, blocks: []block{signedSyslogBlock, syslogBlock}},
	{Name: "snmp_v3", Kind: Packet, Network: "udp", Address: DatagramPort, TLS: TLSForbidden, Authors: RouteAuthors, blocks: []block{snmpBlock}},

	{Name: "redis/pubsub", Kind: Bridge, Network: "tcp", Section: "redis_pubsub"},
	{Name: "redis/streams", Kind: Bridge, Network: "tcp", Section: "redis_streams"},
	{Name: "irc", Kind: Bridge, Network: "tcp", Section: "irc_bridges"},
	{Name: "xmpp", Kind: Bridge, Network: "tcp", Section: "xmpp_bridges"},
	{Name: "sftp", Kind: Bridge, Network: "tcp", Section: "sftp"},
	{Name: "postgres/listen-notify", Kind: Bridge, Network: "tcp", Section: "postgres_notify"},
}

// listenerTransports indexes the listener protocols (every kind but Bridge).
var listenerTransports = func() map[string]Transport {
	index := make(map[string]Transport, len(transports))
	for _, t := range transports {
		if t.Kind != Bridge {
			index[t.Name] = t
		}
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

// AuthorCarrier reports listeners that accept per-request authors (plain
// contract requests naming who posts), and so take the author access policy.
func AuthorCarrier(protocol string) bool {
	t, _ := Lookup(protocol)
	return t.Authors == RequestAuthors
}

// RouteCarrier reports listeners whose sender is verified by the provider
// protocol itself (a pinned TLS syslog certificate, a signed-syslog origin,
// SNMPv3 USM, an SCP login) and which post as a configured or
// provider-derived author. They take no listener access policy (guests,
// password); their dispatcher still applies flood limits.
func RouteCarrier(protocol string) bool {
	t, _ := Lookup(protocol)
	return t.Authors == RouteAuthors
}

// plaintext reports a host:port listener that may serve plaintext off
// loopback, IRC style.
func (t Transport) plaintext() bool { return t.TLS != TLSRequired }

// unixSocket reports the Unix socket carriers.
func (t Transport) unixSocket() bool {
	return t.Address == Path && (t.Network == "unix" || t.Network == "unixpacket" || t.Network == "unixgram")
}

func systemVQueueReply(_ Config, l Listener) bool {
	reply, err := SystemVQueueKey(l.ReplyTo)
	ingress, ingressErr := SystemVQueueKey(l.Address)
	return err == nil && ingressErr == nil && reply != ingress
}

func posixQueueReply(_ Config, l Listener) bool {
	return posixQueueName(l.ReplyTo) && l.ReplyTo != l.Address
}

func sharedMemoryReply(_ Config, l Listener) bool {
	return sharedMemoryName(l.ReplyTo) && l.ReplyTo != l.Address
}

func mappedFileReply(c Config, l Listener) bool {
	return mappedDirectory(l.ReplyTo) && !sharedRootsOverlap(l.ReplyTo, l.Address) && !sharedRootsOverlap(l.ReplyTo, c.StateDir)
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
