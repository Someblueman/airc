# Authenticated opaque history cursors

Enabled HTTP `history.read`, `search` or `watch` requires a dedicated top-level `cursor_key_file`.
Provision it in an existing owner-only directory using the gateway's exclusive
credential creation command:

```sh
go run ./cmd/airc-gateway credential init --file /absolute/private/path/cursor.key
```

The file contains a crypto-random 256-bit key encoded as 64 hexadecimal characters.
It must be distinct from all state, bearer/account, signing/recipient and TLS
references. Startup reads the bounded file with the same native descriptor owner,
private-permission and no-follow checks as bearer credentials, and snapshots it
before journal/listener startup. Nothing creates this key implicitly or stores
it in SQLite. Keep the same provisioned key across process restarts to resume
cursors. Replacing it and restarting invalidates old cursors; no fallback key is
tried. Native non-Unix key-file checks remain unavailable.

The `gc1.` format is canonical unpadded base64url of AES-256-GCM ciphertext.
Go's `cipher.NewGCMWithRandomNonce` generates/prepends the nonce and verifies the
tag. Authenticated associated data has the fixed version domain plus the exact
configured realm, verified principal ID, stable backend binding, operation and
normalized typed target. Search also binds the exact query and normalized sender;
conversation reads also bind the original external parent ID and thread mode.
These optional fields are omitted for ordinary history, preserving its existing
associated data. Switching parent or mode requires a new initial read. The encrypted position is exactly one 32-character
upstream message ID. Payload length, version, canonical encoding, scope and tag
must all match; raw IDs, wildcard/audit selectors, paths and tampering fail.
Under the standard library's random-nonce key limit, provision a replacement key
before issuing 2^32 cursors with one key. Automatic key-usage accounting/rotation
is not implemented.

A cursor never grants authority or extends retention. Every page rechecks current
credential/configured permissions and durable revocation, then obtains a new
bounded one-use read lease. Cross-principal, cross-binding, cross-operation,
cross-realm and cross-target reuse fails even when the recipient is otherwise
authorized for that target. An authenticated old position whose upstream record
was evicted still returns the explicit upstream retention gap. Cursors have no
separate wall-clock expiry; key rotation and current authorization control reuse.
Changing the requested limit does not change selector scope or reset any budget.

Evidence includes a Go-library AES-256-GCM known vector, scope/tamper/canonical
encoding and rotation negatives, same-key snapshot reopening, bounded malformed
cursor fuzzing, and real HTTPS HTTP/2 pagination across an owned CLI restart.
No custom cipher, nonce-generation scheme or compatibility decoder is used.
