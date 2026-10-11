# Gateway open items

Verification that has not happened yet, in the author model. Each item is
done when the carrier posts as a guest and as a registered account through
a real client or device, checked by an independent AIRC reader; then update
its row in the [carriers table](README.md#carriers) and delete the line.

- **Matrix bridge**: run against a real homeserver (only an in-process HTTP
  test server so far).
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
- **WebRTC**: a real remote path through NAT or a TURN relay.
- **Providers**: a real GitHub webhook delivery and a ChirpStack server with
  a radio device.
- **Physical links**: UART over a real serial line, and an offline bundle
  carried on removable media.
