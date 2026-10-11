# Gateway configuration v1

One strict JSON file configures the gateway: where AIRC is (`upstream`), which
listeners accept requests, and what each listener admits. Requests name their
own author, as IRC clients name their nick, so there are no gateway accounts,
tokens or keys to provision. See [authentication.md](authentication.md) for
the author model and [service.md](service.md) for ownership and startup.

```sh
go run ./cmd/airc-gateway config validate --file examples/gateway/config.disabled.json
go run ./cmd/airc-gateway capabilities --file examples/gateway/config.disabled.json
```

`config validate` reports `configuration valid; no runtime or listeners
started`. `capabilities` reports compiled operations and each listener's
status without secrets, paths or endpoints, and creates no state. Both are
read-only and contact nothing. `airc service install` (see
[../SERVICE.md](../SERVICE.md)) writes a working configuration for a local
aircd.

## Schema

The file is a single UTF-8 JSON object of at most 64 KiB. Unknown, duplicate,
case-aliased and null fields, malformed escapes, trailing data and excess
nesting fail. `version` must be 1; `realm` is 1–64 letters, digits, `-` or
`_`; `state_dir` is an absolute normalized path that, if it exists, is
owner-only and not a symlink. Restart is the only reload. Fields of
[removed connection modes](removed-modes.md) fail as unknown. Fields of the
removed identity model (`principals`, `bindings`, keys, return routes, fixed
broker targets) fail with one error pointing to
[removed-identity.md](removed-identity.md).

| Field | Content |
|---|---|
| `upstream` | The AIRC endpoint every author connects through (below) |
| `carriers` | Optional ordered ways to reach this AIRC service, published by `capabilities.get` like aircd's `005 CARRIERS` ([format](../SERVICE.md#carrier-discovery)) |
| `cursor_key_file` | Owner-only key for history, search and watch cursors ([cursors-v1.md](cursors-v1.md)) |
| `listeners` | At most 128 listeners |
| `sftp`, `postgres_notify`, `redis_pubsub`, `redis_streams` | Route-owned ingress posting as [route authors](route-authors.md) |
| `irc_bridges`, `xmpp_bridges`, `matrix_bridges` | Bridges posting provider-verified authors ([author rules](irc-bridge.md#bridge-authors)) |
| `limits`, `retry` | Optional resource and retry settings |

## Upstream

`upstream` has `network` (`unix` or `tcp`) and `address`. A Unix address is an
absolute socket path with no TLS settings. TCP uses a numeric IP and port;
plaintext TCP must be loopback, and TLS needs `tls_server_name` and
`access_token_file` (aircd's listener password), with an optional
`tls_ca_file` (system roots otherwise; there is no insecure switch).

`gateway_token_file` is the owner-only file holding aircd's
`--gateway-token-file` credential (it must differ from the access token).
With it, each author session first sends `WEBIRC <source>`, so aircd applies
its per-address limits to the real client: its IP address, or
`listener:<id>` for carriers without addresses. Sources live only in memory;
after a restart a pending job uses `listener:<id>`. Clients never see the
upstream.

## Listeners

Every listener has a unique `id`, a `protocol`, `enabled` (default false),
`address` (`127.0.0.1:0` when omitted; a path or name for local carriers) and
an access policy. Every policy key is optional, and an empty policy admits
guests from anywhere the listener is reachable:

| Key | Default | Meaning |
|---|---|---|
| `operations` | every available operation | Allowlist. Empty means every implemented operation plus `account.register`; history, search and watches also need `cursor_key_file`. `capabilities.get` and `account.identify` are always available. |
| `targets` | any target | Typed room/direct allowlist; AIRC still decides. |
| `guests` | `true` | `false` requires an account token on every author. |
| `password_file` | none | Owner-only file with one line, the access password (IRC's `PASS`), sent as `password` or the `AIRC-Password` header; once per connection. |
| `limits.requests_per_minute` | 120 | Per client source. |
| `limits.author_requests_per_minute` | 60 | Per author. |
| `limits.connections` | 256 | Open connections on this listener (`limits.sessions` still applies). |
| `limits.connections_per_source` | 16 | Open connections from one IP address (loopback is exempt, as in aircd). |

Exceeded rates answer `capacity_exceeded`; refused connections close before
any protocol work. A missing or wrong password, a guest where guests are off
and a target outside the allowlist answer `forbidden`.

Mailbox, queue, shared-memory, mapped-file, offline and Bundle Protocol
listeners may set `bound_author: {"nick", "account_token_file"}`: requests
that name no author run as that nickname. Queue, shared-memory and mapped-file
listeners need `reply_to`, one owner-provisioned destination of their own kind
for every reply. See [local-authors.md](local-authors.md).

Route-owned ingress (Redis, PostgreSQL, SFTP, SCP, TLS and signed syslog,
SNMPv3, GitHub webhooks, ChirpStack) has no listener policy: its provider
check decides who may use it, and it posts as the request's author or a
configured `bound_author`; see [route-authors.md](route-authors.md).

### TLS and carriers

TLS is optional, on any address, for carriers with a plaintext form (`http`,
`websocket`, `mcp_http`, `framed`, `unix`, `grpc`, `mqtt`, `amqp`, `stomp`,
`coap_tcp`, `zeromq`, `nng`): supply `tls_cert_file` and `tls_key_file`
together. Account tokens sent without TLS can be observed on the network.
Datagram carriers (`udp`, `coap_udp`, `udp_lite`, `ipv4_multicast`,
`ipv6_multicast`, `ipv4_broadcast`, `icmp_v6`, `mqtt_sn`) and SCTP carry plain
author requests and answer the request's own source or association.
Carriers encrypted by definition (`http3`, `webtransport`, `rawquic`,
`rawquic_datagrams`, `dtls`, `coaps`, `coaps_tcp`, `webrtc`, `syslog_tls`)
require a certificate even on loopback. `coap_oscore` uses an
owner-provisioned OSCORE context (`airc-gateway oscore init --listener ID`);
`oscore.author` optionally binds a default author to it. Each carrier's
document gives its framing and bounds; the network carriers are summarized
in [author-network-carriers.md](author-network-carriers.md).

Broker routes do not pin a room. MQTT topic routes, AMQP addresses and STOMP
destinations take any request; the request names its target, limited by the
listener's `targets`. With no routes, MQTT reads topic `airc/requests`, and
AMQP and STOMP read `airc.requests` and answer on `airc.responses`. A route's
optional `operations` must be within the listener's. See [mqtt.md](mqtt.md),
[amqp.md](amqp.md) and [stomp.md](stomp.md).

`unix`, `stdio`, the mailboxes and the other local carriers have
platform-specific path, ownership and sole-owner rules in their documents
([unix-stream.md](unix-stream.md), [stdio.md](stdio.md),
[file-mailbox.md](file-mailbox.md), [offline-bundle.md](offline-bundle.md),
[scp-inbox.md](scp-inbox.md)).

### Browser origins

`http`, `websocket`, `mcp_http` and `webrtc` accept
`"allowed_origins": ["https://app.example"]`. The default rejects every
Origin-bearing request. At most 16 exact canonical HTTP(S) origins are
allowed, plain HTTP origins only for loopback development. See
[browser.md](browser.md).

## Credential files

Referenced files must exist as regular, non-symlink files; private ones
(passwords, tokens, keys) have no group or other permission bits. State and
credential references must be distinct, including aliases and hard links,
except that one file may repeat within a role: listeners sharing a
certificate, key or `password_file` (which may also be the upstream's
`access_token_file`), and a listener certificate that is also the upstream's
`tls_ca_file`. Validation checks permissions, not content; content is checked
at startup. `airc-gateway credential init --file PATH` creates a new private
32-byte key file, for example a `cursor_key_file`.

## Limits and retries

| Setting | Default | Accepted range/constraint |
|---|---:|---|
| `limits.workers` | 8 | 1–64, at most total sessions |
| `limits.sessions` | 32 | 1–256 |
| `limits.sessions_per_author` | 4 | 1–total sessions |
| `limits.watches` | 8 | 0–total sessions minus one |
| `limits.watches_per_author` | 2 | 0–author sessions minus one, at most total watches |
| `limits.pending` | 4096 | 1–65536 |
| `limits.pending_per_author` | 128 | 1–total pending |
| `limits.journal_bytes` | 268435456 | 1 MiB–1 GiB |
| `retry.policy` | `retry` | `retry` or `receipt_only` |
| `retry.send_attempts` | 100 | 1–100 |
| `retry.lookup_attempts` | 100 | 1–100 |
| `retry.lifetime_seconds` | 86400 | 1–2592000 |
| `retry.backoff_seconds` | 1 | At least 1, at most maximum backoff |
| `retry.max_backoff_seconds` | 60 | At most 3600 |
| `retry.terminal_retention_seconds` | 604800 | At least lifetime, at most 7776000 |

Reads (history, search, directory, context, pins, room, waiting, poll
results, topic, members, channels, agents) and WebRTC need at least two
global and per-author sessions, so a read never takes the last send slot.
Watches also need nonzero `watches` and `watches_per_author`. Budgets,
backoff and expiry are fixed when a request is admitted.
