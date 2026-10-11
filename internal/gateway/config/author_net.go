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
