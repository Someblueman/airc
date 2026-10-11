package config

// Old-vs-new equivalence: every protocol-name check the transport registry
// replaces, compared with the registry for every protocol name accepted
// before it (plus unknown names). The legacy predicates are the old code's
// own functions where they existed and verbatim copies of its inline
// chains otherwise (config.go, service.go, cmd/airc-gateway/main.go,
// internal/service/gateway.go). This table is temporary: it is dropped once
// the old code is gone; the golden tests stay.

import (
	"fmt"
	"runtime"
	"slices"
	"testing"
)

var equivalenceNames = append(slices.Clone(corpusProtocols), "irc", "redis/pubsub", "Http", "udp ")

// --- config.go, inline chains copied verbatim ------------------------------

func legacyOrigins(p string) bool {
	return !(p != "http" && p != "http3" && p != "webtransport" && p != "websocket" && p != "mcp_http" && p != "webrtc")
}

// legacyEmptyAddress is what Validate did with an empty address.
func legacyEmptyAddress(p string) string {
	if p == "sctp" || p == "sctp_unordered" || p == "sctp_pr_ttl" || p == "mach_ipc" || p == "icmp_v6" {
		if p == "mach_ipc" {
			return "error: mach_ipc listener requires a bootstrap service name"
		}
		if p == "icmp_v6" {
			return "error: icmp_v6 listener requires an explicit [IPv6]:channel endpoint"
		}
		return "error: SCTP listener requires an explicit IP:port"
	}
	if p == "unix" || p == "unixpacket" || p == "unixgram" || p == "file_mailbox" || p == "directory_mailbox" || p == "append_log" || p == "fifo" || p == "uart" || p == "mapped_file" {
		return "error: local listener requires an absolute path"
	}
	if p != "stdio" && p != "socketpair" && p != "inherited" && p != "offline_bundle" && p != "bpv7" && p != "scp_inbox" {
		return "127.0.0.1:0"
	}
	return ""
}

func legacyNoAddress(p string) bool {
	return p == "stdio" || p == "socketpair" || p == "inherited" || p == "offline_bundle" || p == "bpv7" || p == "scp_inbox"
}

// legacyEndpoint is which endpoint check Validate ran.
func legacyEndpoint(p string) string {
	if p != "shared_memory" && p != "mapped_file" && p != "sctp" && p != "sctp_unordered" && p != "sctp_pr_ttl" && p != "mach_ipc" && p != "icmp_v6" && p != "system_v_queue" && p != "posix_mqueue" && p != "unix" && p != "unixpacket" && p != "unixgram" && p != "stdio" && p != "socketpair" && p != "inherited" && p != "file_mailbox" && p != "directory_mailbox" && p != "append_log" && p != "fifo" && p != "uart" && p != "offline_bundle" && p != "bpv7" && p != "scp_inbox" {
		if p == "ipv4_multicast" || p == "udp_lite" || p == "ipv4_broadcast" || p == "ipv6_multicast" {
			return "group:" + p
		} else if p == "snmp_v3" || p == "mqtt_sn" || p == "udp" || p == "coap_udp" || p == "coap_tcp" || p == "coap_oscore" {
			return "udp"
		}
		return fmt.Sprint("tcp plaintext=", legacyPlaintextCapable(p))
	}
	return "none"
}

func legacyUnixFamily(p string) bool { return p == "unix" || p == "unixpacket" || p == "unixgram" }

func legacyImplemented(p string) bool {
	return !(p != "udp_lite" && p != "ipv4_broadcast" && p != "ipv4_multicast" && p != "ipv6_multicast" && p != "icmp_v6" && p != "nng" && p != "zeromq" && p != "snmp_v3" && p != "syslog_signed" && p != "syslog_tls" && p != "stomp" && p != "amqp" && p != "mqtt_sn" && p != "mqtt" && p != "shared_memory" && p != "mapped_file" && p != "sctp" && p != "sctp_unordered" && p != "sctp_pr_ttl" && p != "mach_ipc" && p != "system_v_queue" && p != "posix_mqueue" && p != "http" && p != "http3" && p != "webtransport" && p != "rawquic" && p != "rawquic_datagrams" && p != "dtls" && p != "coaps" && p != "coaps_tcp" && p != "udp" && p != "coap_udp" && p != "coap_tcp" && p != "coap_oscore" && p != "grpc" && p != "framed" && p != "websocket" && p != "mcp_http" && p != "webrtc" && p != "unix" && p != "unixpacket" && p != "unixgram" && p != "stdio" && p != "socketpair" && p != "inherited" && p != "file_mailbox" && p != "directory_mailbox" && p != "append_log" && p != "fifo" && p != "uart" && p != "offline_bundle" && p != "bpv7" && p != "scp_inbox")
}

func legacyTLSRequired(p string) bool {
	return p == "http3" || p == "webtransport" || p == "rawquic" || p == "rawquic_datagrams" || p == "dtls" || p == "coaps" || p == "coaps_tcp" || p == "webrtc"
}

func legacyUDPNoTLS(p string) bool { return p == "udp" || p == "coap_udp" || p == "coap_tcp" }

// legacySole is the exclusivity group an enabled listener counted toward.
func legacySole(p string) string {
	switch {
	case p == "offline_bundle" || p == "bpv7":
		return "offline"
	case p == "file_mailbox":
		return "file mailbox"
	case p == "stdio" || p == "socketpair" || p == "inherited" || p == "fifo" || p == "uart" || p == "scp_inbox":
		return "parent stream"
	}
	return ""
}

// legacyBlocks are the per-listener validators that treated p as their
// owner, in the order Validate called them.
func legacyBlocks(p string) []block {
	var owned []block
	add := func(b block, owner bool) {
		if owner {
			owned = append(owned, b)
		}
	}
	add(localREPBlock, p == "zeromq" || p == "nng")
	add(fileMailboxBlock, p == "file_mailbox" || p == "directory_mailbox" || p == "append_log")
	add(bundleProtocolBlock, p == "bpv7")
	add(amqpBlock, p == "amqp")
	add(stompBlock, p == "stomp")
	add(snmpBlock, p == "snmp_v3")
	add(signedSyslogBlock, p == "syslog_signed")
	add(syslogBlock, p == "syslog_tls" || p == "syslog_signed")
	add(mqttBlock, p == "mqtt")
	add(mqttSNBlock, p == "mqtt_sn")
	add(offlineBundleBlock, p == "offline_bundle")
	add(uartBlock, p == "uart")
	add(fifoBlock, p == "fifo")
	add(oscoreBlock, p == "coap_oscore")
	add(webRTCBlock, p == "webrtc")
	return owned
}

func legacyBrokerDefaults(p string) bool { return p == "mqtt" || p == "amqp" || p == "stomp" }

func legacySharedMemoryLocalPath(p string) bool {
	switch p {
	case "unix", "unixpacket", "unixgram", "file_mailbox", "directory_mailbox", "append_log", "fifo", "uart":
		return true
	}
	return false
}

func legacyDirectories(p string) []string {
	if p == "directory_mailbox" || p == "append_log" || p == "fifo" {
		names := []string{"", "inbox", "processing", "results"}
		if p == "fifo" {
			names = []string{""}
		}
		if p == "append_log" {
			names = []string{"", "logs"}
		}
		return names
	}
	return nil
}

// --- the old classification functions --------------------------------------

func legacyAuthorCarrier(p string) bool      { return AuthorCarrier(p) }
func legacyRouteCarrier(p string) bool       { return RouteCarrier(p) }
func legacyBoundAuthorCarrier(p string) bool { return boundAuthorCarrier(p) }
func legacyReplyCarrier(p string) bool       { return replyCarrier(p) }
func legacyPlaintextCapable(p string) bool   { return PlaintextCapable(p) }

// --- platform checks, parameterized by platform ----------------------------

// legacyEnabledAvailable is whether configuration accepted an enabled
// listener of p on goos/goarch, by the check that guarded it.
func legacyEnabledAvailable(p, goos, goarch string) bool {
	unixLike := goos == "darwin" || goos == "linux"
	switch p {
	case "unix", "unixgram", "stdio", "socketpair", "inherited", "fifo", "uart", "scp_inbox",
		"file_mailbox", "directory_mailbox", "append_log", "offline_bundle", "bpv7", "coap_oscore",
		"ipv4_multicast", "ipv6_multicast", "ipv4_broadcast":
		return unixLike
	case "unixpacket":
		return goos == "linux"
	case "udp_lite":
		return goos == "linux"
	case "icmp_v6", "mach_ipc":
		return goos == "darwin" && goarch == "arm64"
	case "shared_memory", "mapped_file":
		return goarch == "arm64" && (goos == "darwin" || goos == "linux")
	case "posix_mqueue":
		return goos == "linux" && (goarch == "arm64" || goarch == "amd64")
	case "sctp", "sctp_unordered", "sctp_pr_ttl":
		return goos == "linux" && goarch == "arm64"
	case "system_v_queue":
		return (goos == "darwin" && goarch == "arm64") || (goos == "linux" && (goarch == "amd64" || goarch == "arm64"))
	}
	return true
}

// legacyCapabilitiesListed and legacyCapabilitiesUnavailable copy the
// capabilities command (cmd/airc-gateway/main.go).
func legacyCapabilitiesListed(p string) bool {
	return p == "ipv4_multicast" || p == "ipv6_multicast" || p == "udp_lite" || p == "snmp_v3" || p == "syslog_signed" || p == "syslog_tls" || p == "stomp" || p == "amqp" || p == "mqtt_sn" || p == "shared_memory" || p == "mapped_file" || p == "sctp" || p == "sctp_unordered" || p == "sctp_pr_ttl" || p == "mach_ipc" || p == "mqtt" || p == "posix_mqueue" || p == "http" || p == "http3" || p == "webtransport" || p == "rawquic" || p == "rawquic_datagrams" || p == "dtls" || p == "coaps" || p == "coaps_tcp" || p == "udp" || p == "coap_udp" || p == "coap_tcp" || p == "coap_oscore" || p == "unixgram" || p == "grpc" || p == "framed" || p == "websocket" || p == "mcp_http" || p == "webrtc" || p == "unix" || p == "unixpacket" || p == "scp_inbox" || p == "stdio" || p == "socketpair" || p == "inherited" || p == "file_mailbox" || p == "directory_mailbox" || p == "append_log" || p == "fifo" || p == "uart" || p == "offline_bundle" || p == "bpv7"
}

func legacyCapabilitiesUnavailable(p, goos, goarch string) bool {
	unix := goos == "darwin" || goos == "linux"
	sharedMemory := goarch == "arm64" && (goos == "darwin" || goos == "linux")
	sctp := goos == "linux" && goarch == "arm64"
	mach := goos == "darwin" && goarch == "arm64"
	posix := goos == "linux" && (goarch == "arm64" || goarch == "amd64")
	unixPacket := goos == "linux"
	return (p == "shared_memory" && !sharedMemory) || (p == "mapped_file" && !sharedMemory) || ((p == "sctp" || p == "sctp_unordered" || p == "sctp_pr_ttl") && !sctp) || (p == "mach_ipc" && !mach) || (p == "posix_mqueue" && !posix) || (p == "unixpacket" && !unixPacket) || ((p == "unix" || p == "unixgram" || p == "ipv6_multicast") && !unix) || ((p == "scp_inbox" || p == "stdio" || p == "socketpair" || p == "inherited") && !unix) || ((p == "coap_oscore" || p == "file_mailbox" || p == "directory_mailbox" || p == "append_log" || p == "fifo" || p == "uart" || p == "offline_bundle" || p == "bpv7") && !unix)
}

// The capabilities command never listed these implemented carriers, and
// never reported these two unavailable; the registry keeps both quirks.
var (
	capabilitiesUnlistedQuirk  = []string{"system_v_queue", "ipv4_broadcast", "icmp_v6", "zeromq", "nng"}
	capabilitiesAvailableQuirk = []string{"udp_lite", "ipv4_multicast"}
)

// --- service.go and internal/service ----------------------------------------

func legacyServeRefusal(p string) string {
	switch {
	case p == "scp_inbox":
		return "SCP inbox requires scp-inbox"
	case p == "bpv7":
		return "BPv7 requires import-bpv7"
	case p == "offline_bundle":
		return "offline bundle requires import-bundle"
	case p == "file_mailbox":
		return "file mailbox requires mailbox-once"
	case p == "stdio" || p == "socketpair" || p == "inherited" || p == "fifo" || p == "uart":
		return fmt.Sprintf("%s listener requires serve-%s", p, p)
	}
	return ""
}

// legacyStreamNetwork is how service.go listened for a connection carrier.
func legacyStreamNetwork(p string) string {
	if p == "sctp" || p == "sctp_unordered" || p == "sctp_pr_ttl" {
		return "sctp"
	} else if p == "unix" {
		return "unix"
	} else if p == "unixpacket" {
		return "unixpacket"
	}
	return "tcp"
}

// legacyInstallTLS is internal/service carrierSpecs' tls column.
var legacyInstallTLS = map[string]bool{"http": true, "websocket": true, "mcp_http": true, "framed": true, "unix": false, "grpc": true, "mqtt": true, "amqp": true, "stomp": true, "coap_udp": false, "coap_tcp": false, "zeromq": false, "nng": false, "udp": false}

func registryOf(p string) Transport {
	if t, ok := Lookup(p); ok {
		return t
	}
	return unknownTransport(p)
}

func registryEmptyAddress(t Transport) string {
	switch t.Address {
	case SCTPPort:
		return "error: SCTP listener requires an explicit IP:port"
	case ServiceName:
		return "error: " + t.Name + " listener requires a bootstrap service name"
	case ICMPEndpoint:
		return "error: " + t.Name + " listener requires an explicit [IPv6]:channel endpoint"
	case Path:
		return "error: local listener requires an absolute path"
	case NoAddress:
		return ""
	}
	return "127.0.0.1:0"
}

func registryEndpoint(t Transport) string {
	switch t.Address {
	case GroupPort:
		return "group:" + t.Name
	case DatagramPort:
		return "udp"
	case HostPort:
		return fmt.Sprint("tcp plaintext=", t.plaintext())
	}
	return "none"
}

func registrySole(t Transport) string {
	return map[soleGroup]string{offlineImport: "offline", fileMailboxOwner: "file mailbox", parentStream: "parent stream"}[t.sole]
}

func TestTransportRegistryMatchesLegacyChecks(t *testing.T) {
	platforms := [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "386"}, {"windows", "amd64"}, {"freebsd", "arm64"}}
	for _, p := range equivalenceNames {
		r := registryOf(p)
		_, known := Lookup(p)
		check := func(what string, legacy, registry any) {
			t.Helper()
			if fmt.Sprint(legacy) != fmt.Sprint(registry) {
				t.Errorf("%q %s: legacy %v, registry %v", p, what, legacy, registry)
			}
		}
		check("implemented", legacyImplemented(p), known)
		check("origins", legacyOrigins(p), r.Origins)
		check("empty address", legacyEmptyAddress(p), registryEmptyAddress(r))
		check("no address", legacyNoAddress(p), r.Address == NoAddress)
		check("endpoint", legacyEndpoint(p), registryEndpoint(r))
		check("unix family", legacyUnixFamily(p), r.unixSocket())
		check("sole group", legacySole(p), registrySole(r))
		check("blocks", legacyBlocks(p), r.blocks)
		check("broker defaults", legacyBrokerDefaults(p), r.defaults != nil)
		check("shared memory local path", legacySharedMemoryLocalPath(p), r.Address == Path && p != "mapped_file")
		check("private directories", legacyDirectories(p), r.directories)
		check("author carrier", legacyAuthorCarrier(p), r.Authors == RequestAuthors)
		check("route carrier", legacyRouteCarrier(p), r.Authors == RouteAuthors)
		check("bound author", legacyBoundAuthorCarrier(p), r.BoundAuthor)
		check("reply carrier", legacyReplyCarrier(p), r.replyTo != nil)
		check("serve refusal", legacyServeRefusal(p), map[bool]string{true: r.title + " requires " + r.Command}[r.Kind == Process])
		check("command", legacyServeRefusal(p) != "", r.Command != "")
		check("capabilities listed", legacyCapabilitiesListed(p), known && !slices.Contains(capabilitiesUnlistedQuirk, p))
		if r.Address == HostPort {
			// PlaintextCapable is read only for host:port endpoints.
			check("plaintext", legacyPlaintextCapable(p), r.plaintext())
		}
		// "<p> listener requires TLS certificates" (enabled). TLS syslog is
		// required too, but its own validator refuses a missing
		// certificate first, so the message never changes. Unknown protocols
		// are refused as unimplemented before this check.
		check("tls required", legacyTLSRequired(p), known && r.TLS == TLSRequired && r.Authors != RouteAuthors)
		// "udp listener cannot use TLS or origins" (enabled). SNMP, MQTT-SN
		// and OSCORE refuse TLS and origins in their own validators first.
		check("udp without tls", legacyUDPNoTLS(p), r.Address == DatagramPort && r.TLS == TLSForbidden && len(r.blocks) == 0)
		if r.Kind == Stream {
			network := r.Network
			if network != "sctp" && network != "unix" && network != "unixpacket" {
				network = "tcp"
			}
			check("stream network", legacyStreamNetwork(p), network)
		}
		if tls, ok := legacyInstallTLS[p]; ok {
			check("install tls", tls, r.TLS == TLSOptional)
		}
		for _, pf := range platforms {
			check("enabled available on "+pf[0]+"/"+pf[1], legacyEnabledAvailable(p, pf[0], pf[1]), availableOn(r.Platforms, pf[0], pf[1]))
			if legacyCapabilitiesListed(p) {
				check("capabilities unavailable on "+pf[0]+"/"+pf[1], legacyCapabilitiesUnavailable(p, pf[0], pf[1]), !availableOn(r.Platforms, pf[0], pf[1]) && !slices.Contains(capabilitiesAvailableQuirk, p))
			}
		}
	}
	// This platform's old availability functions.
	for p, legacy := range map[string]func() bool{
		"unix": UnixAvailable, "stdio": StdioAvailable, "unixpacket": UnixPacketAvailable, "icmp_v6": ICMPv6Available,
		"mach_ipc": MachIPCAvailable, "shared_memory": SharedMemoryAvailable, "mapped_file": MappedFileAvailable,
		"posix_mqueue": POSIXQueueAvailable, "sctp": SCTPAvailable, "system_v_queue": systemVQueueAvailable,
	} {
		if legacy() != Available(p) {
			t.Errorf("%s available on %s/%s: legacy %t, registry %t", p, runtime.GOOS, runtime.GOARCH, legacy(), Available(p))
		}
	}
	// Bridges, in the order the service started them.
	var bridges []string
	for _, t := range Transports() {
		if t.Kind == Bridge {
			bridges = append(bridges, t.Name)
		}
	}
	if fmt.Sprint(bridges) != "[redis/pubsub redis/streams irc xmpp sftp postgres/listen-notify]" {
		t.Errorf("bridges %v", bridges)
	}
}

// The reply_to checks keep the old per-protocol switch's results.
func TestReplyToMatchesLegacySwitch(t *testing.T) {
	c := corpusConfig()
	for _, p := range []string{"posix_mqueue", "system_v_queue", "shared_memory", "mapped_file"} {
		for _, address := range []string{"/airc-in", "4242", "/private/mapped-in", "/private/state/x"} {
			for _, reply := range []string{"/airc-in", "/airc-out", "4242", "4343", "/private/mapped-in", "/private/mapped-out", "/private/state/y", "bad"} {
				l := Listener{Protocol: p, Address: address, ReplyTo: reply}
				ok := false
				switch l.Protocol {
				case "posix_mqueue":
					ok = posixQueueName(l.ReplyTo) && l.ReplyTo != l.Address
				case "system_v_queue":
					reply, err := SystemVQueueKey(l.ReplyTo)
					ingress, ingressErr := SystemVQueueKey(l.Address)
					ok = err == nil && ingressErr == nil && reply != ingress
				case "shared_memory":
					ok = sharedMemoryName(l.ReplyTo) && l.ReplyTo != l.Address
				case "mapped_file":
					ok = mappedDirectory(l.ReplyTo) && !sharedRootsOverlap(l.ReplyTo, l.Address) && !sharedRootsOverlap(l.ReplyTo, c.StateDir)
				}
				if got := registryOf(p).replyTo(c, l); got != ok {
					t.Errorf("%s %s reply %s: legacy %t, registry %t", p, address, reply, ok, got)
				}
			}
		}
	}
}
