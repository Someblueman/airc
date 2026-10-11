# Gateway configuration v1

The strict parser, local CLI, journal/backend workers and partial HTTP, framed TCP/TLS, WebSocket, Unix, stdio and MCP HTTP services
are implemented. HTTP send/reply/room reaction.add/context.read/pins.read/room.read/waiting.read/
poll.results/topic.read/members.read/channels.read/local receipt, room history/search/watch, SSE, public
directory reads, self profile/presence and room topic/settings mutations can be enabled; framed TCP/TLS and WebSocket carry the same current operations; richer operations and other adapters remain pending. This is a staged implementation,
not catalogue completion. The existing `http` listener also exposes the
[JSON-RPC profile](jsonrpc.md) under the same operation allowlist. See
[service.md](service.md) for ownership and startup.

From the gateway worktree, with no installation or live AIRC access:

```sh
go run ./cmd/airc-gateway config validate --file examples/gateway/config.disabled.json
go run ./cmd/airc-gateway capabilities --file examples/gateway/config.disabled.json
```

The first command reports `configuration valid; no runtime or listeners started`.
The second returns `runtime: "partial_not_running"`, compiled send/reply/reaction.add/context.read/pins.read/room.read/waiting.read/
poll.results/topic.read/members.read/channels.read/receipt.get/history.read/search/watch/events.watch/directory/
profile.update/presence.set/topic.set/room.update/pins.add/pins.remove/activity.set/reply_signal.set/reply_signal.cancel/poll.create/poll.vote/poll.close/message.action/message.correct/message.retract operations, and disabled status with empty configured operations for
the example listener. No secrets, account identities, endpoints, or filesystem paths appear
in capability output. The example has no account or gateway credentials and
creates no state directory. It is intentionally inactive.

Both commands load the same bounded, strict schema intended for daemon startup.
`config validate --file PATH` and `capabilities --file PATH` are read-only;
extra arguments and unknown flags fail. Validation does not contact any upstream.
The separate `credential init --file PATH` command provisions a new private
gateway credential; see [authentication.md](authentication.md) for its ownership,
no-overwrite, and platform requirements.

The `stdio` listener has no address, TLS or origin settings and must be the sole enabled listener. Run it with `serve-stdio --file PATH` over dedicated stdin/stdout pipes; ordinary `serve` rejects it before journal creation. See [stdio.md](stdio.md) for framing, authentication, EOF and platform boundaries.

The `scp_inbox` listener uses an OpenSSH forced `scp-inbox --file CONFIG` command over dedicated pipes. It maps an explicit login UID to one encrypted signed Send principal/target and must be the sole enabled owner. See [scp-inbox.md](scp-inbox.md) for account restrictions, the exact legacy SCP profile and durable acknowledgement semantics.

`protocol: "chirpstack"` selects the TLS-protected ChirpStack 4 HTTP JSON
integration. Configure a private bearer secret and exact tenant/application/
DevEUI/FPort-to-principal Send routes; see [chirpstack.md](chirpstack.md).

The `file_mailbox` listener names an existing private directory and is processed
one document at a time with `mailbox-once --file PATH`. It must be the sole
enabled listener, with explicit send/receipt.get/history.read permissions and
signed or encrypted intents. See [file-mailbox.md](file-mailbox.md) for atomic
claim/result behavior, termination evidence and remaining platform qualification.


The `offline_bundle` ingress has no address, TLS or origins, allows only explicit
`send`, and must be the sole enabled ingress. Use
`import-bundle --file CONFIG --input /absolute/path/BUNDLE`; ordinary `serve`
refuses it before journal creation. All entries require signed sender authority;
durable per-entry reports distinguish new intake from rejected replays. See
[offline-bundle.md](offline-bundle.md) for partial-batch and platform boundaries.

## Schema

The file is a single UTF-8 JSON object, at most 64 KiB. Unknown, duplicate,
case-aliased and null fields, malformed Unicode escapes, trailing objects and
excess nesting fail. Arrays may be omitted or empty; explicit null is rejected.
`version` must be 1; `realm` is a 1–64-character identifier using letters, digits,
hyphen and underscore. `state_dir` must be an absolute normalized path. An
existing state directory must be owner-only and must not be a symlink.
Validation never creates it. Restart is the only planned configuration reload.
Fields of [removed connection modes](removed-modes.md) are unknown and fail.

| Field | Content |
|---|---|
| `upstream` | Optional AIRC endpoint for per-request authors, with aircd's gateway credential (below) |
| `carriers` | Optional ordered list of ways to reach this AIRC service, published by `capabilities.get` exactly as aircd's `005 CARRIERS` ([format](../SERVICE.md#carrier-discovery)) |
| `bindings` | At most 256 immutable AIRC destination/identity bindings |
| `principals` | At most 1024 stable gateway identities |
| `recipient_keys` | At most eight provisioned P-256 private decryption keys |
| `response_signing_key` | Optional owner Ed25519 response signer, required by enabled UDP |
| `listeners` | At most 128 explicitly configured adapter instances |
| `sftp` | At most 16 pinned OpenSSH account mailboxes with signed/encrypted requests and fixed encrypted responses; [profile](sftp.md) |
| `postgres_notify` | At most 16 PostgreSQL LISTEN/NOTIFY subscriptions with certificate authentication and fixed encrypted signed Send grants; [profile](postgres-notify.md) |
| `matrix_bridges` | At most 32 fixed trusted homeserver owners; every route requires an encrypted response recipient key and the global response signer, including Send-only routes; [profile](matrix-bridge.md#service-ownership-and-encrypted-replies) |
| `return_routes` | At most 1024 owner-provisioned result destinations |
| `limits` | Optional bounded resource settings |
| `retry` | Optional retry and retention settings |

Bindings require unique `id`, `network` (`unix` or `tcp`), explicit `address`,
valid AIRC `account`, and absolute `account_token_file`. Optional references are
`access_token_file`, `tls_ca_file`, and `tls_server_name`. Unix bindings require
an absolute socket path and no TLS settings. TCP uses an explicit numeric IP
and port; plaintext endpoints must be loopback. TLS requires a server name and
access-token file. With no custom CA, later startup must use system trust roots;
no insecure TLS switch is defined. The parser does not silently choose the
live daemon's address. The journal freezes and checks bindings for admitted jobs before allowing
configuration changes to affect dispatch.

## Upstream and author access

`upstream` names the AIRC endpoint every per-request author connects through:
`network` (`unix` or `tcp`), `address`, and optional `access_token_file`,
`tls_server_name` and `tls_ca_file` with the same rules as a binding, plus
`gateway_token_file`: the owner-only file holding aircd's
`--gateway-token-file` credential (it must differ from the access token).
With it, each author session first sends `WEBIRC <source>`, so aircd applies
its per-address limits (registrations, unregistered connections, failed
tokens) to the real client: its IP address without the port, or
`listener:<id>` for carriers without addresses. Sources stay in memory; after a
restart a pending job's attempts use `listener:<id>`. Without `upstream`,
authors use the endpoint all bindings share, without WEBIRC. Clients never see
the upstream.

Listeners that carry plain author requests (`http`, `websocket`, `mcp_http`,
`framed`, `unix`) take an IRC-style access policy. Every key is optional, and
zero configuration admits guests from anywhere the listener is reachable:

| Key | Default | Meaning |
|---|---|---|
| `operations` | every available operation | Shared with principals; when empty, authors get every implemented operation plus `account.register` (history, search and watches need `cursor_key_file`). `capabilities.get` and `account.identify` are always available. |
| `targets` | any target | Typed allowlist for author requests; AIRC still decides. |
| `guests` | `true` | `false` requires an account token on every author. |
| `password_file` | none | Owner-only file with one line: the access password (IRC's server `PASS`), sent as `password` or the `AIRC-Password` header; once per connection. |
| `limits.requests_per_minute` | 120 | Per client source. |
| `limits.author_requests_per_minute` | 60 | Per author (nickname scope). |
| `limits.connections` | 256 | Open connections on this listener (the global `limits.sessions` still applies). |
| `limits.connections_per_source` | 16 | Open connections from one IP address (loopback is exempt, as in aircd). |

Exceeded rates answer `capacity_exceeded`; refused connections are closed
before any protocol work. A missing or wrong password, a guest where guests are
off and a target outside the allowlist answer `forbidden`.

Principals require unique `id`, an existing `binding`, a `bearer_token_file` or
nonempty `signing_keys`,
and explicit `operations` and `targets` allowlists (omission means empty/default
deny). `revoked` defaults to false. Operation names use the request contract;
invalid or repeated names fail. Targets use typed room/direct destinations,
normalize only direct nicknames, and reject duplicates after normalization.
Each principal may list at most 256 exact targets; there are no wildcards or
all-DM audit selectors. Parsing alone does not authenticate callers or enforce durable revocation.
An explicit `directory` operation grants global public-card discovery on the bound
backend even with no chat targets; see [directory.md](directory.md).
Explicit `profile.update` and `presence.set` permissions mutate only the bound
author card, even with no chat targets; see [profile-presence.md](profile-presence.md).
Runtime authentication and revocation are provided by the separate auth/store paths. Signed and
encrypted key schemas and codec limits are in [signed-message-v1.md](signed-message-v1.md).
Explicit `topic.set` permission changes only an authorized exact room header;
see [topic-set.md](topic-set.md).
`room.update` additionally requires native operator/admin authority for the
configured account; gateway permission cannot grant it. See [room-update.md](room-update.md).
`pins.add` and `pins.remove` require exact room permission and a native ID/room
proof before writing. Native posting restrictions apply independently; see
[pin-mutations.md](pin-mutations.md).

Listeners contain unique `id`, a `protocol` name, `enabled` (false by default),
`address` (loopback `127.0.0.1:0` if omitted), and an `operations` allowlist.
Optional `tls_cert_file` and `tls_key_file` must be supplied together. TLS is
optional, on any address, for protocols with a plaintext form (`http`,
`websocket`, `mcp_http`, `framed`, `unix`, `mqtt`, `amqp`, `stomp`), as on IRC;
account tokens sent without TLS can be observed on the network. Bearer
principals and signed envelopes over HTTP, WebSocket and MCP still require TLS
or loopback, and MQTT, AMQP, STOMP and TLS syslog still require certificates
until they carry author requests. Protocols encrypted by definition (`http3`,
`webtransport`, `rawquic`, `dtls`, `coaps`, `coaps_tcp`, `webrtc`, `grpc`)
keep requiring TLS. Other non-loopback network addresses require TLS except for
`udp`, `coap_udp` and `coap_tcp`, which normally require encrypted signed
envelopes and provisioned protected return routes. UDP also has an explicit
[anonymous mode](anonymous-udp.md) with source-address replies. Enabled `http`, `http3`, `webtransport`, `rawquic`,
`rawquic_datagrams`, `dtls`, `coaps`, `coaps_tcp`, `udp`, `coap_udp`, `coap_tcp`, `grpc`, `framed`, `websocket`, `webrtc`, `unix`, `stdio` and `mcp_http`
permit only implemented send/reply/reaction.add/context.read/pins.read/room.read/
waiting.read/poll.results/topic.read/members.read/channels.read/receipt.get/history.read/search/watch/events.watch/directory/
profile.update/presence.set/topic.set/room.update/pins.add/pins.remove/activity.set/reply_signal.set/reply_signal.cancel/poll.create/poll.vote/poll.close/message.action/message.correct/message.retract operations (see
[framed-stream-v1.md](framed-stream-v1.md), [websocket-v1.md](websocket-v1.md) and
[unix-stream.md](unix-stream.md) and [mcp-http.md](mcp-http.md)); other enabled protocols and unimplemented
operations fail. Unix requires an explicit absolute normalized filesystem
socket path, even when disabled. Origin options are rejected; enabled Unix
also rejects TLS options. Enabled Unix
is unavailable outside Darwin/Linux and is rejected before runtime state is
opened. Its private directory, same-UID peer and cleanup rules are in the Unix
adapter document; network endpoint defaults do not apply to it.
Enabled `ipv6_multicast` uses an explicit group/interface and encrypted AGD1
requests with a fixed protected unicast response route; TLS and origins are
rejected. See [IPv6 multicast](multicast-ipv6.md) for configuration and qualified
platforms.
Enabled `http3` requires certificate and key even on loopback. It binds UDP and
uses TLS 1.3 with `h3` ALPN; see [http3.md](http3.md).
Enabled `webtransport` also requires TLS certificate/key and binds UDP. It uses
the dedicated signed/encrypted session contract in [webtransport.md](webtransport.md).
Enabled `webrtc` requires TLS certificate/key even on loopback and uses
TLS 1.3 HTTPS offer/answer signaling. Browser access requires exact
`allowed_origins`; native clients may omit Origin. Optional `webrtc` settings
configure `ice_servers` and `ice_transport_policy` (`all` or `relay`). TURN
credentials use private absolute `credential_file` paths, never literal JSON
passwords. Reserve at least two global and per-principal sessions for signaling
and peer admission. Other listener protocols reject these options. See
[webrtc.md](webrtc.md) for channel modes, authentication and bounded framing.
Enabled `dtls` requires an RSA or ECDSA TLS certificate/key, including on
loopback, and binds UDP. It uses DTLS 1.2, exact ALPN and the detached AGD1
signed/encrypted request contract in [dtls.md](dtls.md). It rejects Origin
configuration; browser HTTP origins are not DTLS identities.

Enabled `coaps` requires an RSA or ECDSA certificate/key and binds UDP with
DTLS 1.2 and `coap` ALPN. It rejects Origin configuration. Requests use
signed/encrypted envelopes in CoAP POST; live reads use session-bound Observe
registration. See [coap.md](coap.md) for resources and wire bounds.

Enabled `coaps_tcp` requires a certificate/key, including on loopback, and
binds TCP with TLS 1.3 and `coap` ALPN. It rejects Origin configuration.
It uses the same detached POST resources and association-bound Observe
registration as `coaps`, after the reliable-transport capability exchange.
See [coap-tcp.md](coap-tcp.md) for negotiation and carrier limits.

By default, enabled `udp` uses bounded authenticated AGD1 datagrams without TLS or Origin
fields. It requires `response_signing_key` and exactly one fixed encrypted
return route per configured principal. Listener binds may use numeric wildcard
addresses and port `0`; route destinations must be numeric unicast addresses
with nonzero ports. See [udp.md](udp.md) for the JOSE response envelope,
route fields, fragmentation limits, retries, and current acceptance boundary.

Setting the UDP listener's `anonymous_principal` to an existing principal ID
selects plaintext JSON requests and replies to each source socket. No client
credentials or fixed return routes are needed. The principal supplies the shared
native author and target permissions; it may omit credentials. Only `send`,
`receipt.get`, and `history.read` are supported. See
[anonymous-udp.md](anonymous-udp.md) for the wire contract, shared identity, and
response-size limits.

Enabled `coap_udp` uses plain CoAP NON POSTs carrying encrypted AGD1 fragments.
It requires the same response signer and fixed recipient/destination fields as
UDP, with route `protocol: "coap_udp"` matching the listener. TLS and Origin
fields are rejected. See [coap-udp.md](coap-udp.md) for its protected mailbox,
fragment resources and honest loss semantics.

Enabled `coap_tcp` uses plaintext RFC 8323 connections with encrypted signed
requests and signed/encrypted results. It requires the response signer and a
matching `coap_tcp` recipient route per principal, with
`destination: "connection"`; no return address is selected from caller input.
It rejects TLS and Origin fields. See [coap-plain-tcp.md](coap-plain-tcp.md).

Enabled `rawquic` and `rawquic_datagrams` require TLS certificate/key, bind UDP
and use `airc-gateway-quic/1` ALPN. They reject HTTP Origin configuration;
see [quic.md](quic.md) for framing, datagram negotiation and acceptance status.

Enabled `grpc` requires certificate and key even on loopback and rejects origin
options. It uses native generated gRPC clients over HTTP/2 TLS; see [grpc.md](grpc.md).
`members.read` requires exact room permission and a bounded native NAMES
snapshot, with no cursor key or delivery job; see [members.md](members.md).
`channels.read` is explicit global room discovery, including room names and
metadata outside message-target permissions. It does not expand those target
permissions; see [channels.md](channels.md).
`activity.set` requires exact room or direct permission, explicit typing/thinking
TTL (1–15 seconds) and a bounded note. Native throttle acknowledgements can
retain earlier intent; see [activity.md](activity.md).
`reply_signal.set` and `reply_signal.cancel` require exact room or direct permission
and a retained parent message. Set requires a note of at most 240 bytes and TTL
1–900 seconds; cancel has no input object. See [reply-signals.md](reply-signals.md).
Enabled history/search/watch/events.watch requires a dedicated absolute `cursor_key_file` and at least two
global and per-principal sessions, preserving send capacity. Provisioning and
cursor rotation semantics are in [cursors-v1.md](cursors-v1.md). Enabled watch or events.watch also
requires nonzero global and per-principal watch limits; watches consume bounded
lookup sessions and preserve a send slot. See [watch.md](watch.md) and [live-events.md](live-events.md). Enabled
directory, channels.read, context.read, pins.read, room.read, waiting.read,
poll.results, topic.read or members.read also requires at least two global and per-principal sessions, but does
not require a cursor key or watch quota.
Disabled unknown protocol names describe proposed instances, not implementations. New transport-native configuration will be added with
each concrete adapter; an IP endpoint is not a universal native IPC/device address.

Return routes have unique `id`, an existing `principal`, `protocol`, and bounded
`destination` with no CR/LF/NUL. These are fixed owner-provisioned references;
requests cannot override them. Their concrete scheme, topic, filesystem and
authentication checks remain coupled to the relevant adapter implementation.
No return route is opened or advertised by the current CLI.

UDP routes additionally require `listener`, `recipient_key_id`, and an absolute
`recipient_public_key_file`. Their listener must name a UDP listener and their
destination is a numeric unicast `host:port`. Other route protocols reject
these UDP-only fields. See [udp.md](udp.md).

Credential references must exist as regular, nonsymlink files. Private files
must have no group/other permission bits. State and all configured credential
references must be distinct, including aliases and hard links, except that one
file may repeat within a single role: listeners sharing a TLS certificate, key or
`password_file`, which may also be the upstream's `access_token_file` (one
connection credential), and a listener certificate that is also the upstream
`tls_ca_file`. A file in two roles still fails. File content,
current-owner enforcement, certificate verification and safe credential loading
are still required at authentication/backend startup; permission validation alone
is not proof of a valid credential. Configuration itself must be a regular,
nonsymlink file. The command reads at most the configured byte bound.

## Limits and retries

Omitted scalar settings inherit the defaults below. Explicit zero or out-of-range
values fail unless zero watches is deliberately configured with both watch
limits set to zero. Limits are configuration promises until the journal and
scheduler implement and verify enforcement.

| Setting | Default | Accepted range/constraint |
|---|---:|---|
| `limits.workers` | 8 | 1–64, at most total sessions |
| `limits.sessions` | 32 | 1–256 |
| `limits.sessions_per_principal` | 4 | 1–total sessions |
| `limits.watches` | 8 | 0–total sessions minus one |
| `limits.watches_per_principal` | 2 | 0–principal sessions minus one, at most total watches |
| `limits.pending` | 4096 | 1–65536 |
| `limits.pending_per_principal` | 128 | 1–total pending |
| `limits.journal_bytes` | 268435456 | 1 MiB–1 GiB |
| `retry.policy` | `retry` | `retry` or `receipt_only` |
| `retry.send_attempts` | 100 | 1–100 |
| `retry.lookup_attempts` | 100 | 1–100, independent of send attempts |
| `retry.lifetime_seconds` | 86400 | 1–2592000 |
| `retry.backoff_seconds` | 1 | At least 1, at most maximum backoff |
| `retry.max_backoff_seconds` | 60 | At most 3600 |
| `retry.terminal_retention_seconds` | 604800 | At least lifetime, at most 7776000 |

The session constraints leave send capacity when watches are full. Ordinary
posts default to bounded `retry`, with stable IDs and permitted duplicate risk
under uncertain delivery. The store/scheduler persists budgets,
backoff, expiry and defaults once at admission; validating these numbers alone
does not perform delivery or recovery.

## Browser origins

HTTP, WebSocket and MCP HTTP listeners optionally accept `"allowed_origins":["https://app.example"]`.
The default is empty and rejects all Origin-bearing requests. At most 16 exact,
canonical ASCII HTTP(S) origins are permitted, each at most 512 bytes; no paths,
credentials, wildcards, opaque origins or explicit default ports. Plain HTTP
origins are limited to loopback development. Other protocols cannot configure
this field. Policies are validated and copied at startup; changing the JSON file
does not change a running listener. See [browser.md](browser.md) for client setup
and separate authentication requirements.

`poll.create`, `poll.vote` and `poll.close` require exact room permission.
Creation bounds question/options/TTL; vote and close resolve the native poll ID
against that room before writing. See [polls.md](polls.md).
