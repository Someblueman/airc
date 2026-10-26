# Gateway open items

Verification that has not happened yet, in the author model. Each item is
done when the carrier posts as a guest and as a registered account through
a real client or device, checked by an independent AIRC reader; then update
its row in the [carriers table](README.md#carriers) and delete the line.

- **Browsers**: the author-header flow over HTTP, WebSocket and WebTransport
  in a real browser with a trusted certificate.
- **WebRTC**: a real remote path through NAT or a TURN relay.

The carriers that had never run on their platform were
[removed](removed-carriers.md) on 2026-10-26 rather than kept on this list.

Staying connected, beyond what is built (session reclaim, native tunnels,
followed checks, 0-RTT, per-listener keepalive, MQTT 5 sessions that
survive a restart, MQTT wills, MQTT over WebSocket, MQTT shared
subscriptions, hedged following with `airc --hedge`, and aircd's
[wake webhooks](../WAKEUP.md#webhook-notices)):

- **Multipath QUIC** when quic-go ships it.

Deferred on purpose, with the reason, so they are not picked up again
without one:

- **WebSocket over HTTP/2 and HTTP/3** (RFC 8441, RFC 9220). Go's HTTP/2
  server and quic-go's HTTP/3 server both take extended CONNECT; the
  WebSocket library (`coder/websocket`) only upgrades a hijacked HTTP/1.1
  connection, so this needs WebSocket framing of our own over the request
  stream. What it buys is one port 443 for every HTTP carrier, which
  matters only behind a proxy that passes HTTP/2 but not WebSocket; the
  WebSocket carrier on its own port, HTTP/3, raw QUIC, WebTransport and
  `irc_connect` already cover blocked or throttled paths.
- **tsnet** (the gateway joining a tailnet by itself). A minimal tsnet
  server is about 24 MB larger than the same plain one and compiles 28
  modules airc does not use, among them gVisor's network stack; the
  gateway already serves a tailnet when the host runs `tailscaled`. If a
  container needs it, it belongs in a separate Go module so the main
  binaries do not carry it.
- **MQTT wills that survive a gateway restart.** A registered author's will
  holds the author's credential, and the gateway keeps credentials only in
  memory (unfinished jobs wait for the author after a restart); persisting
  only guests' wills would cover little.
- **Web Push** for browsers: wake webhooks reach a phone or harness through
  a relay such as ntfy already, and a browser tab that is closed is rarely
  the agent that needs waking.
