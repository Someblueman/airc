# Gateway open items

Verification that has not happened yet, in the author model. Each item is
done when the carrier posts as a guest and as a registered account through
a real client or device, checked by an independent AIRC reader; then update
its row in the [carriers table](README.md#carriers) and delete the line.

- **Darwin ARM64 carriers**: Mach IPC, the XPC and D-Bus bridges, QR image
  sequences, shared memory, mapped files and ICMPv6.
- **Linux ARM64 carriers**: SCTP (all three modes), shared memory and mapped
  files.
- **POSIX message queues**: run on a Linux host with `/dev/mqueue`.
- **Packet carriers**: UDP-Lite, IPv4 multicast, IPv6 multicast (Linux needs
  a multicast-capable interface, not loopback) and IPv4 broadcast.
- **Native OSCORE**: build the worker and run it on Linux and Darwin.
- **Browsers**: the author-header flow over HTTP, WebSocket and WebTransport
  in a real browser with a trusted certificate.
- **Native tunnels**: an IRC client with a proxy setting (WeeChat) on
  `irc_connect`, as a guest and a registered account; gamja has run on
  `irc_websocket`.
- **WebRTC**: a real remote path through NAT or a TURN relay.
- **Physical links**: UART over a real serial line, and an offline bundle
  carried on removable media.

Staying connected, beyond what is built (session reclaim, native tunnels,
followed checks, 0-RTT):

- **WebSocket over HTTP/2 and HTTP/3** (RFC 8441, RFC 9220), so one port
  443 serves every HTTP carrier; the WebSocket library has no extended
  CONNECT yet.
- **Per-listener idle and keepalive settings** for WebSocket pings and QUIC
  idle timeouts, tuned for NAT; today they are fixed (10 s pings, 30 s idle).
- **Multipath QUIC** when quic-go ships it.
- **MQTT 5 sessions**, **Web Push and wake webhooks**, and **dual-carrier
  following in the client**, as planned.
