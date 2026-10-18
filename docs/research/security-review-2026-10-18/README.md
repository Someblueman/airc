# Security review of the stay-connected work

18 October 2026. Two independent reviews read everything merged for the
[stay-connected plan](../stay-connected-2026-10-17/README.md) (`a3a8b1d`
to `8f500d0`): one aircd's session reclaim and wake notices, the newest
outbound surface; one the gateway's MQTT 5 sessions, wills and WebSocket
listener, native tunnels, follows and checks, keepalive and 0-RTT. Each
finding was checked against the code before it was fixed, and every code
fix but the once-a-minute pruning has a test that fails without it.

## aircd

| Finding | Severity | Outcome |
| --- | --- | --- |
| A saved wake registration the daemon no longer accepts (its account deleted, its URL off a narrowed allowlist) stopped aircd starting, and a deleted account's live connection could keep writing one | Medium | Dropped with a warning on start; deleting an account drops its registration; `WAKE` needs an account that still exists |
| The URL check refused `..` and `%` but not `..;`, `//` or `;`, which some receivers resolve outside a path-scoped prefix; a prefix without a trailing `/` matched `/aircX` | Medium/low | A wake path is letters, digits and `-._~/` with no empty or dot-led segment and no query; prefixes end in `/` |
| `last_error` showed the transport error: resolved addresses, resolver and proxy addresses, certificate names | Low | `WAKE STATUS` names the kind of failure; the log keeps the error |
| Every inbox message rescanned all registrations, parsing each URL, under the server lock | Low | Pruning runs once a minute |
| A broad public prefix lets a few accounts spend the relay's per-address rate limit for all | Low | Documented in [WAKEUP.md](../../WAKEUP.md#webhook-notices) |
| The HMAC key (the decoded secret, not the hex), replays, `HTTPS_PROXY` | Info | Documented |

Session reclaim and `RESUME` were found sound: 128-bit secrets compared in
constant time, issued only to the session, cleared on takeover, and
`identityAllowedLocked` still guarding nicks. Two clients sharing one
account credential now take the nick from each other rather than one
getting `433`; that is the design, and it needs the credential.

The fault-injection soak, extended in the same change, found one more:
news waiting out a quiet period or a receiver's backoff lived only in
memory, so a restart lost the notice until the next message. It is now
saved with the registration.

## Gateway

| Finding | Severity | Outcome |
| --- | --- | --- |
| Any client could take an MQTT client ID: kick its connection, delete its session and fire its will at once, repeatably | Medium | An identified connection or unexpired session belongs to its author; anyone else is refused (`0x87`, or CONNACK 2 on 3.1.1) and nothing changes; a connection without an author has no will delay |
| Raw QUIC ran 0-RTT streams and datagrams before the handshake completed: replayable, from unproven source addresses that per-source and per-address limits count | Medium | Nothing is handled until the handshake completes; HTTP/3 and WebTransport already refused early requests |
| An MQTT 5 follow blocked on PUBACK, holding backend session and lookup slots for as long as the client kept pinging | Medium | A follow waits 30 s for an acknowledgement, then ends the connection; the session keeps what was unacknowledged |
| One HTTP/2 connection carried up to 250 `irc_connect` tunnels, each its own aircd connection | Medium/low | One stream per HTTP/2 connection |
| `irc_websocket` and `irc_connect` ran without `gateway_token_file`, so aircd saw every client as the local gateway, exempt from its per-address limits | Medium/low | Configuration requires the token for native carriers |
| One author could fill the listener's 1024 MQTT sessions | Low/medium | 16 per author |
| A guest-owned session resumed by the next holder of the nick replayed results of requests made with another author's credential | Low | Those replies go at QoS 0, never kept; a guest's rooms and inbox are already open to whoever holds the nick |
| The sessions file was opened before its type was checked (a FIFO hung startup) and its sessions were not held to the live bounds; 65,535 inflight IDs would loop `nextIDLocked` forever | Low (needs state-dir write access) | Checked before opening; sessions outside the bounds are dropped. Unacknowledged bodies stay in the file, mode `0600` |
| A property identifier in more than one byte read as its low byte | Nit | Refused |

The review also found `docs/gateway/http.md` claiming HTTP/3 serves 0-RTT
requests, which the handler answers `425`; the docs now say so.

Checked and found sound: the MQTT 5 decoder's bounds, response topics
confined to the publishing session, subscription authorization re-checked
every lease, will admission, the snapshot's atomic write, the WebSocket
origin checks, CONNECT ignoring the requested authority, slot accounting
in follows, `fitCheck` and `boundedCheck`, and keepalive timers.
