# Gateway configuration

One strict JSON file configures the gateway: where AIRC is (`upstream`),
which listeners accept requests, and what each listener admits. There are no
gateway accounts, tokens or keys to provision; requests name their own
author ([authentication.md](authentication.md)).
`airc service install --gateway ...` writes a working file for a local aircd
([SERVICE.md](../SERVICE.md#bundled-gateway)).

```json
{
  "version": 1,
  "realm": "home",
  "state_dir": "/home/me/.local/state/airc/gateway",
  "upstream": {"network": "unix", "address": "/home/me/.local/state/airc/airc.sock",
               "gateway_token_file": "/home/me/.local/state/airc/gateway.token"},
  "cursor_key_file": "/home/me/.local/state/airc/gateway-cursor.key",
  "listeners": [
    {"id": "web", "protocol": "http", "enabled": true, "address": "127.0.0.1:6680"},
    {"id": "ws", "protocol": "websocket", "enabled": true, "address": "127.0.0.1:6681",
     "targets": [{"kind": "room", "name": "#lobby"}], "guests": false}
  ]
}
```

```sh
airc-gateway config validate --file /abs/gateway.json
airc-gateway capabilities --file /abs/gateway.json
```

`config validate` parses and checks the file and prints `configuration
valid; no runtime or listeners started`. `capabilities` prints the compiled
operations and each listener's status (`disabled`, `disabled_unavailable`
on a platform the carrier does not run on, `enabled_<protocol>_partial` with
the operations its authors may request; an unknown protocol is
`disabled_unimplemented`) without secrets.
Neither creates state or contacts anything. `examples/gateway/` has minimal
files.

## Top level

The file is one UTF-8 JSON object of at most 64 KiB and ten nesting levels.
Unknown, duplicate, case-aliased and null fields, malformed escapes and
trailing data fail. Fields of [removed connection modes](removed-modes.md)
fail as unknown; fields of the removed identity model (`principals`,
`bindings`, keys, return routes, fixed broker targets) fail with one error
naming the field and [removed-identity.md](removed-identity.md). Restart is
the only reload.

| Field | Content |
|---|---|
| `version` | `1` |
| `realm` | 1–64 letters, digits, `-` or `_`; part of every job and cursor identity |
| `state_dir` | Absolute normalized path of the journal directory (below) |
| `upstream` | The AIRC endpoint every author connects through; required to post |
| `carriers` | Optional ordered list published by `capabilities.get`, in aircd's `005 CARRIERS` format ([discovery](../SERVICE.md#carrier-discovery)) |
| `cursor_key_file` | Owner-only 32-byte key for history, search and watch cursors |
| `listeners` | At most 128 listeners |
| `redis_pubsub`, `redis_streams`, `postgres_notify`, `sftp` | Route-owned ingress ([brokers](brokers.md), [offline](offline.md#sftp-mailbox)) |
| `irc_bridges`, `xmpp_bridges` | Chat bridges ([bridges](bridges.md)) |
| `limits`, `retry` | Optional resource and retry settings (below) |

## Upstream

`upstream.network` is `unix` or `tcp`. A Unix `address` is an absolute
socket path with no TLS settings. A TCP address is a numeric IP and port;
plaintext TCP must be loopback. TLS needs `tls_server_name` and
`access_token_file` (aircd's listener password), with an optional
`tls_ca_file` (system roots otherwise; there is no insecure switch).

`gateway_token_file` holds aircd's `--gateway-token-file` credential and
must differ from the access token. With it, each author session first
sends `WEBIRC <source>`, so aircd applies its per-address limits to the real
client: its IP address, `listener:<id>:pid:<pid>` for a Unix socket
client process, `listener:<id>` for other carriers without addresses,
`route:<id>` for route-owned ingress, or `bridge:<id>:<nick>` for a bridged
user. Sources live only in memory; after a restart a pending job uses its
listener's source.

## Listeners

Each listener has a unique `id` (1–64 letters, digits, `-`, `_`), a
`protocol`, `enabled` (default `false`) and an `address`. Network
carriers default to `127.0.0.1:0` (port chosen at startup and reported in
the ready record); SCTP and ICMPv6 need an explicit address. Network
addresses are numeric. Local carriers take a path or name; `stdio`,
`socketpair`, `inherited`, `offline_bundle`, `bpv7` and `scp_inbox` take
none. Enabled listeners may not share an address.

Every listener that carries author requests takes this access policy, all
optional; the empty policy admits guests from anywhere the listener is
reachable. The [native tunnels](native.md) (`irc_websocket`, `irc_connect`)
take only the two connection limits: AIRC admits their clients itself.

| Key | Default | Meaning |
|---|---|---|
| `operations` | every available operation | Allowlist. History, search and watches also need `cursor_key_file`. `capabilities.get` and `account.identify` are always allowed. |
| `targets` | any target | Up to 256 rooms or nicknames; AIRC still decides. |
| `guests` | `true` | `false` requires an account token on every author. |
| `password_file` | none | Owner-only file with one line, the access password (IRC's `PASS`), sent as `password` or the `AIRC-Password` header, once per connection. |
| `limits.requests_per_minute` | 2400 | Per client source (0–100000); loopback sources are exempt, as in aircd. On a Unix socket each client process is its own source. |
| `limits.author_requests_per_minute` | 1200 | Per author, on every carrier. |
| `limits.connections` | 1024 | Open connections on this listener (0–4096); the global `limits.connections` still applies. |
| `limits.connections_per_source` | 16 | Open connections from one IP address; loopback is exempt, as in aircd. |

Exceeded rates answer `capacity_exceeded`, and refused connections close
before any protocol work. A wrong or missing password, a guest where guests
are off and a target outside `targets` answer `forbidden`; an operation
outside `operations` answers `unsupported_operation`.

Some listeners narrow `operations`:

- Exactly `["send"]`: `offline_bundle`, `bpv7`, `scp_inbox`, `mqtt_sn`,
  `snmp_v3`, `syslog_tls`, `syslog_signed`.
- Explicit list required: `fifo`, `uart`, and the mailboxes (`file_mailbox`,
  `directory_mailbox`, `append_log`, which allow only `send`, `receipt.get`
  and `history.read`).
- Mutations only: `mqtt` (a publication gets no reply).
- `send`, `context.read`, `history.read`, `search` only: `amqp`, `stomp`, and
  the Redis, SFTP and bridge routes.

Enabled reads (history, search, directory, context, pins, room, waiting,
poll, topic, members, channels, agents) and WebRTC need at least two global
and per-author sessions; watches also need nonzero `watches` and
`watches_per_author`.

### TLS

TLS is optional, on any address, for carriers with a plaintext form:
`http`, `websocket`, `mcp_http`, `framed`, `grpc`, `mqtt`, `amqp`, `stomp`
and `uart`. Supply `tls_cert_file` and `tls_key_file` together. As on IRC,
account tokens sent without TLS can be read on the network. `unix`,
`coap_tcp`, `zeromq`, `nng` and the datagram carriers (`udp`, `coap_udp`,
`udp_lite`, multicast, broadcast, `icmp_v6`, `mqtt_sn`, `snmp_v3`, SCTP)
refuse TLS settings. Carriers that are encrypted by definition (`http3`,
`webtransport`, `rawquic`, `rawquic_datagrams`, `dtls`, `coaps`,
`coaps_tcp`, `webrtc`) and the TLS provider listeners (`syslog_tls`,
`syslog_signed`) require a certificate even on loopback. `coap_oscore` uses an OSCORE context instead
([network](network.md#oscore)).

### Per-carrier options

| Option object | Listener | Page |
|---|---|---|
| `allowed_origins` | `http`, `http3`, `webtransport`, `websocket`, `mcp_http`, `webrtc` | [http](http.md#browsers) |
| `webrtc` | `webrtc` | [network](network.md#webrtc) |
| `mqtt`, `amqp`, `stomp`, `mqtt_sn` | brokers | [brokers](brokers.md) |
| `udp_lite`, `ipv4_multicast`, `ipv6_multicast`, `ipv4_broadcast`, `icmp_v6` | packet carriers | [network](network.md#udp-family) |
| `oscore` | `coap_oscore` | [network](network.md#oscore) |
| `bound_author` | mailboxes, queues, shared memory, mapped file, `offline_bundle`, `bpv7` | [authentication](authentication.md#bound-authors) |
| `reply_to` | `posix_mqueue`, `system_v_queue`, `shared_memory`, `mapped_file` (required) | [streams](streams.md#queues-shared-memory-and-mapped-files) |
| `posix_queue`, `shared_memory`, `mach_ipc` | local IPC | [streams](streams.md) |
| `scp_inbox`, `bundle_protocol` | `scp_inbox`, `bpv7` | [offline](offline.md) |
| `syslog`, `signed_syslog`, `snmp` | provider ingress | [bridges](bridges.md) |

`stdio`, `socketpair`, `inherited`, `fifo`, `uart`, `scp_inbox`,
`file_mailbox`, `offline_bundle` and `bpv7` run under their own command and
must be the only enabled listener in their file.

## Credential files

Passwords, tokens, keys and certificates are referenced by absolute path.
They must exist as regular, non-symlink files; private ones (passwords,
tokens, keys) must be owned by the gateway user with no group or other
permission bits, in an owner-only directory. Reads are bounded and errors
never include contents. State and credential paths must be distinct,
including aliases and hard links, except that one file may repeat within a
role: listeners may share a certificate, key or `password_file` (which may
also be the upstream's `access_token_file`), and a listener certificate may
also be the upstream's `tls_ca_file`. Validation checks permissions; content
is checked at startup. `airc-gateway credential init --file PATH` creates a
new private 32-byte hex key (mode 0600) without overwriting anything; use it
for `cursor_key_file`.

## Limits and retries

| Setting | Default | Range |
|---|---:|---|
| `limits.workers` | 8 | 1–64, at most `sessions` |
| `limits.connections` | 4096 | 1–16384 open client connections across every listener |
| `limits.sessions` | 256 | 1–1024 upstream AIRC sessions in use at once by requests; with `waits`, it also bounds each listener's requests in flight (`sessions` + `waits`) |
| `limits.sessions_per_author` | 4 | 1–`sessions` |
| `limits.waits` | 1024 | 0–4096 `check` requests waiting for news at once; 0 answers every check at once |
| `limits.idle_sessions` | 512 | 0–4096 upstream sessions kept between requests for reuse; 0 opens one per request |
| `limits.watches` | 64 | 0 to `sessions` − 1 |
| `limits.watches_per_author` | 2 | 0 to `sessions_per_author` − 1, at most `watches` |
| `limits.pending` | 4096 | 1–65536 unfinished jobs |
| `limits.pending_per_author` | 128 | 1–`pending` |
| `limits.journal_bytes` | 268435456 | 1 MiB–1 GiB |
| `retry.policy` | `retry` | `retry` or `receipt_only` |
| `retry.send_attempts` | 100 | 1–100 |
| `retry.lookup_attempts` | 100 | 1–100 |
| `retry.lifetime_seconds` | 86400 | 1–2592000 |
| `retry.backoff_seconds` | 1 | ≥ 1, at most the maximum |
| `retry.max_backoff_seconds` | 60 | at most 3600 |
| `retry.terminal_retention_seconds` | 604800 | lifetime–7776000 |

An upstream session that finished a send or a read cleanly is kept for
the same author's next request, so a busy agent does not log in to AIRC
for every request. A kept session serves only the author scope, token and
client source it logged in with, at most `sessions_per_author` of them per
author; watches never use one. Before reuse the gateway pings it, and for a
guest checks with `WHO` that no connected client has taken the nickname. A
session closes after 5 seconds idle or 30 seconds after its login, so a
revoked token or a nickname AIRC would now refuse takes effect within that
time. Kept sessions, sessions in use, waiting checks and watches all count
toward aircd's `--max-connections`: at the defaults, up to about 1900 of
aircd's 4096. A read that
finds every session slot in use waits up to a second for one before it
answers `capacity_exceeded`, so a burst, such as many followers woken by
one message, queues instead of failing.

Budgets, backoff and expiry are fixed when a request is admitted. Workers
take at most one job per author at a time, rotating between authors.

## Running

`airc-gateway serve --file CONFIG` validates credentials and certificates,
takes the journal's exclusive lock, binds every enabled listener and prints
one ready record:

```json
{"version":1,"state":"ready","listeners":[{"id":"web","address":"127.0.0.1:6680"}]}
```

Readiness means bound listeners and running workers, not that AIRC is
reachable: mutations queue durably while it is down and deliver when it
returns. SIGINT or SIGTERM stops admission, closes listeners, gives HTTP
five seconds to drain, joins every handler and worker (each worker has five
more seconds to save what it observed) and closes the journal; pending jobs
resume on the next start. A startup failure closes anything already bound.

Other commands own one carrier each: `serve-stdio`, `serve-socketpair`,
`serve-inherited`, `serve-fifo`, `serve-uart`, `mailbox-once`, `scp-inbox`,
`import-bundle`, `import-bpv7` and `oscore init` (see the carrier pages).

### Journal

`state_dir` must already exist, owned by the gateway user and owner-only.
The journal is SQLite with an exclusive `owner.lock`; on Linux it must be on
ext4, XFS or Btrfs, on macOS on a local filesystem. Background maintenance
expires idle jobs and deletes finished ones after their retention. The
journal is pinned to its `realm`.

The schema is one version (SQLite `user_version` 27) with no migrations; an
empty journal is created at it. A journal written by an earlier gateway
(any lower version) refuses to open and is left unchanged: drain its jobs
with the previous binary, then start this one with a new state directory.
