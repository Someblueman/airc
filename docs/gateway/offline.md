# Files, mailboxes and offline import

These carriers move [contract](contract-v1.md) requests as files: local
mailbox directories, an SFTP or SCP account, or a bundle carried by hand.
Producers that should not keep a token in a file can rely on the listener's
[`bound_author`](authentication.md#bound-authors). Every carrier writes
into the same journal, so a re-read file, a re-imported bundle or a retry on
another carrier posts once. Local mailboxes allow only `send`,
`receipt.get` and `history.read`, listed explicitly.

## Local mailboxes

All three use an existing owner-only (0700) directory with trusted ancestors
and no symlinks; the gateway holds an exclusive lock on it. Inputs must be
regular 0600 files owned by the gateway user with one link, written
completely, synced and renamed into place. A file holds one
[frame](streams.md#frames) without a length prefix (no `stream`); results
are response frames of at most 64 KiB, never overwritten. A result means
durable intake, not AIRC acceptance: ask `receipt.get` later.

| `protocol` | Layout | How it runs |
|---|---|---|
| `file_mailbox` | `request.json` → claimed as `processing.json` → `response.json` | `airc-gateway mailbox-once --file CONFIG` handles one request and exits; it must be the only enabled listener |
| `directory_mailbox` | `inbox/`, `processing/`, `results/` (each 0700); inputs named `<id>.json` with `<id>` 1–64 letters, digits, `-`, `_` and equal to the frame's `correlation_id`; result under the same name | polled by `serve` every 100 ms; at most 128 inbox and 128 result files |
| `append_log` | `logs/<20-digit generation>.jsonl`, one frame per line (≤ 16384 bytes); an empty line seals a generation (≤ 8 MiB); the gateway keeps `claim.json`, `cursor.json` and publishes `response.json` | polled by `serve`; the producer only appends, seals, then starts the next generation |

A claimed input survives interruption and is replayed through the journal
on the next run. An unread result blocks the next claim (for the directory
mailbox, the next claim of that name), which bounds what the gateway writes.
Consume the result, then publish the next request.

## SFTP mailbox

Top-level `sftp` (up to 16 owners) polls one mailbox on an existing OpenSSH
server (SFTP v3 with `fsync@openssh.com`) over SSH with pinned Ed25519 host
and client keys; agents, passwords, certificates and proxies are not used.

```json
"sftp": [{"id": "device", "enabled": true, "address": "192.0.2.10:22",
  "username": "airc_device", "private_key_file": "/abs/device-ssh-key",
  "host_key_file": "/abs/server-ed25519.pub", "root": "/mailbox", "remote_uid": 1001,
  "route": {"target": {"kind": "room", "name": "#lobby"}, "operations": ["send", "history.read"],
            "bound_author": {"nick": "device", "account_token_file": "/abs/device.token"}}}]
```

`root` is a remote 0700 directory owned by `remote_uid`. The producer
uploads a bare request (1–16384 bytes) under a temporary name and renames it
to `request.json`. The gateway answers with `result.json`
(`{"version":1,"protocol":"sftp","request_sha256","request_id","target","result"|"error"}`,
correlated by the SHA-256 of the uploaded bytes), written through
`result.tmp` and a non-overwriting rename. It never deletes `request.json`:
the producer removes `request.json`, then `result.json`, before the next
request. A leftover `result.tmp` stops the mailbox until an operator removes
it. Requests must use the route's target and operations
(`send`, `context.read`, `history.read`, `search`); the source is
`route:<id>`. Anyone who can write the mailbox can post as its bound author.

## SCP inbox

`protocol: "scp_inbox"` runs as an OpenSSH forced command for one dedicated
account, accepting one `send` per upload with `scp -O`:

```json
{"id": "alice-scp", "protocol": "scp_inbox", "enabled": true, "operations": ["send"],
 "scp_inbox": {"login_uid": 1001, "target": {"kind": "room", "name": "#general"},
               "bound_author": {"nick": "alice", "account_token_file": "/etc/airc/alice.token"}}}
```

```text
restrict,command="/opt/airc/bin/airc-gateway scp-inbox --file /etc/airc/alice-scp.json" ssh-ed25519 AAAA... alice-upload
```

```sh
scp -O -i upload_key request.json airc_upload_alice@gateway.example:inbox
```

The command checks that its effective UID equals `login_uid` (nonzero),
that `SSH_ORIGINAL_COMMAND` is exactly `scp -t inbox`, and that the only
control line is `C0600 <size> request.json` (1–16384 bytes). The final SCP
acknowledgement means durable intake; without it, resend the same request.
The account needs `restrict`, no PTY or forwarding, and administrator-owned
keys and configuration; anyone holding an authorized key can post as the
bound author.

## Offline bundles

`protocol: "offline_bundle"` (no address, `operations: ["send"]`, the only
enabled listener) imports a file carried by any means:

```json
{"version": 1, "requests": [
  {"version": 1, "request_id": "r1", "operation": "send",
   "target": {"kind": "room", "name": "#lobby"}, "message": "hello",
   "author": {"nick": "field-station"}}]}
```

```sh
airc-gateway import-bundle --file CONFIG --input /abs/bundle.json \
  [--author-nick NICK [--author-token-file PATH]]
```

A bundle holds 1–32 `send` requests and is at most 1 MiB. Every entry is
checked before the journal opens; any invalid entry refuses the whole
bundle. Intake is durable per entry. The report on stdout lists each entry
with its `author`, `imported`, `replay_rejected` and `result`; an entry
already in the journal is `replay_rejected` with its original result, and
the command then exits nonzero. Delivery gets five seconds before the
command exits; pending jobs stay in the journal for the next import. The
source file is never modified.

## QR image sequences

`scripts/build-qr-tool.sh --output DIR` builds a Darwin ARM64 Swift tool
([`native/qr`](../../native/qr)) that encodes a bundle of up to 64 KiB into
PNG QR codes and decodes a complete set (in any order) back into the bundle
for `import-bundle`:

```sh
DIR/qr-tool encode --input bundle.json --output /abs/fresh-dir
DIR/qr-tool decode --output /abs/private/bundle.json /abs/received/*.png
```

The tool treats the bundle as opaque bytes. It has not been run with author
bundles; camera capture was [removed](removed-modes.md).

## BPv7

`protocol: "bpv7"` imports one RFC 9171 bundle with
`airc-gateway import-bpv7 --file CONFIG --input FILE` (same author flags as
`import-bundle`). The listener has `operations: ["send"]` and a mapping:

```json
"bundle_protocol": {"source": "dtn://producer/", "destination": "dtn://gateway/send",
                    "target": {"kind": "room", "name": "#room"}}
```

The payload (≤ 32 KiB, bundle ≤ 64 KiB) is one bare `send` request. The
bundle's source and destination must equal the mapping's, the request's
target the mapping's target, and its `request_id` the bundle identity:
`bp7-` plus the unpadded base64url SHA-256 of `airc-bpv7-identity-v1`, NUL,
the source EID, NUL, and the creation time (milliseconds) and sequence
number as 64-bit big-endian integers. A re-imported bundle is therefore a replay. Optional
`issued_at` and `expires_at` must equal the bundle's creation time and
expiry, in whole seconds. Only a primary block (with its CRC) and one
payload block are accepted; fragments, extension blocks, administrative records and
BPSec are refused. There is no bundle router or remote receipt.
