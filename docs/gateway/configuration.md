# Gateway configuration v1

The strict parser, local CLI, journal/backend workers and partial HTTP, framed TCP/TLS, WebSocket, Unix and stdio services
are implemented. HTTP send/reply/room reaction.add/context.read/pins.read/room.read/waiting.read/
poll.results/topic.read/local receipt, room history/search/watch, SSE, public
directory reads, and self profile/presence mutations can be enabled; framed TCP/TLS and WebSocket carry the same current operations; richer operations and other adapters remain pending. This is a staged implementation,
not catalogue completion. See [service.md](service.md) for ownership and startup.

From the gateway worktree, with no installation or live AIRC access:

```sh
go run ./cmd/airc-gateway config validate --file examples/gateway/config.disabled.json
go run ./cmd/airc-gateway capabilities --file examples/gateway/config.disabled.json
```

The first command reports `configuration valid; no runtime or listeners started`.
The second returns `runtime: "partial_not_running"`, compiled send/reply/reaction.add/context.read/pins.read/room.read/waiting.read/
poll.results/topic.read/receipt.get/history.read/search/watch/directory/
profile.update/presence.set operations, and disabled status with empty configured operations for
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

## Schema

The file is a single UTF-8 JSON object, at most 64 KiB. Unknown, duplicate,
case-aliased and null fields, malformed Unicode escapes, trailing objects and
excess nesting fail. Arrays may be omitted or empty; explicit null is rejected.
`version` must be 1; `realm` is a 1–64-character identifier using letters, digits,
hyphen and underscore. `state_dir` must be an absolute normalized path. An
existing state directory must be owner-only and must not be a symlink.
Validation never creates it. Restart is the only planned configuration reload.

| Field | Content |
|---|---|
| `bindings` | At most 256 immutable AIRC destination/identity bindings |
| `principals` | At most 1024 stable gateway identities |
| `recipient_keys` | At most eight provisioned P-256 private decryption keys |
| `listeners` | At most 128 explicitly configured adapter instances |
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

Listeners contain unique `id`, a `protocol` name, `enabled` (false by default),
`address` (loopback `127.0.0.1:0` if omitted), and an `operations` allowlist.
Optional `tls_cert_file` and `tls_key_file` must be supplied together. Non-loopback
network addresses require TLS. Enabled `http`, `framed`, `websocket` and `unix`
permit only implemented send/reply/reaction.add/context.read/pins.read/room.read/
waiting.read/poll.results/topic.read/receipt.get/history.read/search/watch/directory/
profile.update/presence.set operations (see
[framed-stream-v1.md](framed-stream-v1.md), [websocket-v1.md](websocket-v1.md) and
[unix-stream.md](unix-stream.md)); other enabled protocols and unimplemented
operations fail. Unix requires an explicit absolute normalized filesystem
socket path, even when disabled. Origin options are rejected; enabled Unix
also rejects TLS options. Enabled Unix
is unavailable outside Darwin/Linux and is rejected before runtime state is
opened. Its private directory, same-UID peer and cleanup rules are in the Unix
adapter document; network endpoint defaults do not apply to it.
Enabled history/search/watch requires a dedicated absolute `cursor_key_file` and at least two
global and per-principal sessions, preserving send capacity. Provisioning and
cursor rotation semantics are in [cursors-v1.md](cursors-v1.md). Enabled watch also
requires nonzero global and per-principal watch limits; watches consume bounded
lookup sessions and preserve a send slot. See [watch.md](watch.md). Enabled
directory, context.read, pins.read, room.read, waiting.read, poll.results or topic.read also requires at least two global and per-principal sessions, but does
not require a cursor key or watch quota.
Disabled unknown protocol names describe proposed instances, not implementations. New transport-native configuration will be added with
each concrete adapter; an IP endpoint is not a universal native IPC/device address.

Return routes have unique `id`, an existing `principal`, `protocol`, and bounded
`destination` with no CR/LF/NUL. These are fixed owner-provisioned references;
requests cannot override them. Their concrete scheme, topic, filesystem and
authentication checks remain coupled to the relevant adapter implementation.
No return route is opened or advertised by the current CLI.

Credential references must exist as regular, nonsymlink files. Private files
must have no group/other permission bits. State and all configured credential
references must be distinct, including aliases and hard links. File content,
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

HTTP and WebSocket listeners optionally accept `"allowed_origins":["https://app.example"]`.
The default is empty and rejects all Origin-bearing requests. At most 16 exact,
canonical ASCII HTTP(S) origins are permitted, each at most 512 bytes; no paths,
credentials, wildcards, opaque origins or explicit default ports. Plain HTTP
origins are limited to loopback development. Other protocols cannot configure
this field. Policies are validated and copied at startup; changing the JSON file
does not change a running listener. See [browser.md](browser.md) for client setup
and separate authentication requirements.
