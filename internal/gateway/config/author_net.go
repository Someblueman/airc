package config

// authorNetCarrier lists the network carriers that take plain author
// requests: gRPC, QUIC, HTTP/3, WebTransport, WebRTC, CoAP (UDP, TCP, DTLS,
// TLS and native OSCORE), generic DTLS, the UDP family, ICMPv6, SCTP,
// ZeroMQ, NNG and MQTT-SN.
func authorNetCarrier(protocol string) bool {
	switch protocol {
	case "grpc", "http3", "webtransport", "rawquic", "rawquic_datagrams", "webrtc",
		"coap_udp", "coap_tcp", "coaps", "coaps_tcp", "coap_oscore", "dtls",
		"udp", "udp_lite", "ipv4_multicast", "ipv6_multicast", "ipv4_broadcast", "icmp_v6",
		"sctp", "sctp_unordered", "sctp_pr_ttl", "zeromq", "nng", "mqtt_sn":
		return true
	}
	return false
}

// LegacyRoutes reports whether a listener still has protected return routes,
// the temporary principal shim for packet and record carriers. Without them a
// carrier takes plain author requests and replies to their source (or on the
// connection) with plain contract JSON; no principal or key is needed.
func (c Config) LegacyRoutes(listener string) bool {
	for _, route := range c.ReturnRoutes {
		if route.Listener == listener {
			return true
		}
	}
	return false
}
