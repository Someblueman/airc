# Gateway signed and encrypted message profiles v1

The pinned codecs and provisioned-key verification feed the shared dispatcher
and bounded HTTP routes. `POST /v1/envelopes/signed` accepts compact JWS and
`POST /v1/envelopes/encrypted` accepts nested compact JWE, with
`Content-Type: application/jose`. The exact compact bytes belong in the body;
whitespace, JSON wrapping and profile fallback are not supported.

These routes use the signer as their sole identity. Do not supply Authorization,
Idempotency-Key or Last-Event-ID headers: mixed credentials and unsigned operation
or cursor overrides fail. Normal listener TLS/local-loopback, ingress concurrency,
Origin and query restrictions still apply. Signing-only principals need no bearer.
A configured exact origin may preflight POST with Content-Type, but actual requests
must still pass signature, configured operation/target and current revocation checks.

The response is version 1 with `operation` and exactly one of `result` (delivery
or scoped receipt), `page` (room history/search/long-poll watch), `directory`
(public snapshot/nickname query) or `capabilities` (caller discovery). Send/reply returns durable `202` and scoped
Location; reads return `200`. Errors use the existing typed HTTP error response.
Authentication failures challenge with the private `AIRC-JWS` or `AIRC-JWE`
scheme for these body profiles; they never ask for a bearer which the route rejects.
The whole encoded response is bounded to 64 KiB, including this wrapper.
Bodies allow 32768 signed or 65536 encrypted bytes. These response-union routes
bound dispatch/read processing to four seconds; the existing HTTP
header/body/read/write deadlines also apply. Stream routes use the separate
setup, lease and write bounds described below.
Other transport profiles and complete catalogue acceptance remain required pending work.
The WebCrypto browser example and isolated Chrome evidence are in [browser.md](browser.md).

For signing-only discovery, sign a request with `operation: "capabilities.get"`,
version 1 and a request ID, with no operation arguments. It needs no separate
permission and is not a configurable AIRC operation, matching bearer discovery.
The response reads current caller permissions and durable revocation locally;
it creates no job or upstream session. Watch modes describe this detached route
(`long_poll` and `sse`), and upstream compatibility remains unknown (`null`).

Read selectors and opaque cursors live inside the authenticated request. Reads
reuse current authorization, pinned bindings, one-use leases and session cleanup;
the read context cannot outlive signed expiry plus the documented clock allowance.
There is no delivery job, cursor file or budget reset for a read. Directory's
explicit permission remains global public-card discovery on the bound backend.
Initial history/search reads use retained history; initial watch uses recent
history. An initial history/watch request may set signed `start` to `recent` or
`retained`. Continuations carry the operation-scoped opaque cursor inside the signed
request and cannot include `start`.

`POST /v1/envelopes/signed/events` and `/v1/envelopes/encrypted/events` accept
the same compact bodies and credential restrictions, with `operation: "watch"`.
They return SSE page frames rather than the response union above. Each lease
uses one upstream session, scoped opaque event IDs, bounded frames/writes and
joined cleanup as described in [events.md](events.md). It ends at the earlier
of the normal 15-second read lease and signed expiry plus clock allowance.
Renew by signing a new envelope with the last complete page's cursor; unsigned
Last-Event-ID and URL selectors are rejected. Expiry ends a started stream with
`lease_expired`; typed errors and profile-specific challenges apply before any
page. A non-watch request never admits a delivery job on these stream routes.

## Signed envelope

Use compact JWS with exactly `alg: "EdDSA"` and a configured `kid` in its
protected header. Only Ed25519 public keys are provisioned. Other headers,
algorithms, embedded keys, remote key references, duplicate fields, and unsigned
payloads are rejected. The signature verifies the received payload bytes before
strict JSON parsing or request normalization.

The payload is a strict object containing `version: 1`, `audience` equal to the
configured realm, integer Unix-second `issued_at` and `expires_at`, and `request`
using [contract-v1.md](contract-v1.md). Expiry must follow issuance; lifetime may
not exceed configured `retry.lifetime_seconds`. Both future issuance and expired
validity have a 60-second clock allowance. Send/reply requests must explicitly
repeat the same validity values inside the request. Reads use outer validity;
their request contract does not permit inner validity fields. Configured exact
operation/target permissions apply after authentication.

Compact JWS is bounded to 32768 bytes and its encoded protected header to 1024
bytes. Payload JSON remains bounded to the request envelope's 16384-byte limit.
These are codec ceilings, not promises that a packet transport can carry them.
Each adapter must enforce its actual payload ceiling and the plan's explicit
fragmentation policy. Plain JWS supplies authentication but no confidentiality;
ingress must also meet its required TLS/local-trust or encrypted profile.

## Encrypted envelope

Use compact JWE with exactly `alg: "ECDH-ES"`, `enc: "A256GCM"`, configured
recipient `kid`, and an `epk` containing only `kty: "EC"`, `crv: "P-256"`,
and canonical 32-byte base64url coordinates. The encrypted-key segment is empty
for direct ECDH-ES. No compression, key URLs, algorithm substitution, or extra
header fields are accepted. Compact JWE is bounded to 65536 bytes and its encoded
protected header to 2048 bytes.

The decrypted plaintext must be the compact signed envelope above. Decryption
alone never identifies a sender; anyone with the recipient public key can encrypt.
The same signature, audience, validity, revocation snapshot and permission checks
apply. Failed encryption/authentication does not fall back to plaintext.

## Provisioned keys

A principal may omit `bearer_token_file` if it has `signing_keys`, an array of
at most eight `{ "id": "signer", "public_key_file": "/absolute/key.json" }`
entries. Signing IDs are unique across the realm. Public files are strict JWK
objects with `kty: "OKP"`, `crv: "Ed25519"`, and canonical 32-byte base64url `x`.
Optional `alg`, `use`, and `kid` must be `EdDSA`, `sig`, and the configured ID
respectively. Private JWK fields are rejected. The same public material cannot
resolve to different principals. Rotation preserves principal/binding identity
and stable upstream IDs; rebuilding the verifier is still the only reload model.

Top-level `recipient_keys` contains at most eight unique
`{ "id": "recipient", "private_key_file": "/absolute/key.pem" }` entries.
Private files must contain exactly one unencrypted PKCS#8 `PRIVATE KEY` PEM
with a P-256 ECDSA key. Key files are bounded to 8192 bytes. Native Unix descriptor
ownership/no-symlink checks apply. Public trust files and their immediate parents
must be owned by the effective user and not group/other writable; private key
files and their parents must have no group/other permissions. Paths must remain
distinct from state and other credential references. Non-Unix key loading fails
closed pending native ACL support. The gateway does not generate signing or
recipient keys yet.

## Replay and runtime requirements

`AuthenticatedMessage.ReplayUntil` is signed expiry plus clock allowance. The
journal intake API atomically coalesces principal/request IDs and intent
fingerprints and records replay markers. The shared dispatcher passes this deadline into the journal admission transaction.
Signed, encrypted and bearer repeats of the same explicit intent coalesce without
resetting validity or budgets. Key rotation does not change principal/binding scope.
Current durable revocation and parent/read filtering are integrated. Broader
authentication-flood controls and complete transport acceptance remain pending.

## Evidence

The implementation pins [go-jose v4.1.5](https://github.com/go-jose/go-jose/releases/tag/v4.1.5)
and restricts its supported algorithms at both parsing and key-loading boundaries.
Tests verify the published [RFC 8037 Appendix A](https://datatracker.ietf.org/doc/html/rfc8037#appendix-A)
Ed25519 vector, generated signed/encrypted round trips, normalization after
verification, time/skew boundaries, read validity, unauthorized/revoked senders,
duplicate JSON, unknown/extra headers, payload/tag tampering, wrong recipient,
untrusted inner signatures, key ambiguity, unsafe public-file modes and wrong
recipient curves. Compact parsing has seeded fuzz coverage.

```sh
go test -race ./internal/gateway/auth
go test ./internal/gateway/auth -run '^$' -fuzz FuzzCompactAuthentication -fuzztime 5s
```

The dependency's own JWE/AES-GCM/ECDH and malformed-key tests were also executed
from its pinned module-cache directory. This is native macOS codec evidence,
not an ingress, device, Windows ACL, or delivery acceptance claim.

Full repository tests/race checks, vet, command builds, and Linux/Windows
cross-builds passed. A `govulncheck v1.8.0` scan found no affected symbols or
imported packages. It reported the existing `golang.org/x/sys v0.41.0` module's
Windows-only `GO-2026-5024` advisory in an unimported package; no advisory was
reported for the new JOSE dependency. The unrelated dependency was left unchanged.

Real TLS HTTP/1.1 and HTTP/2 fixtures verify signed send, encrypted replay/reply,
bearer coalescing, native independent observation and all currently implemented
read operations. Negative routes cover tampering, wrong profiles, mixed/unsigned
headers, target permission and revocation. A withheld native snapshot with unrelated
traffic proves cancellation at signed validity. An actual standalone CLI with a
signing-only principal accepts signed and encrypted posts and capability discovery over TLS HTTP/2 and
joins shutdown. No live AIRC account or installed daemon is used.

Real signed/encrypted streams over HTTP/1.1 and HTTP/2 verify multiline live
messages, opaque signed-cursor resume, retained-prefix renewal, signed expiry,
current revocation and rejection of unsigned overrides/non-watch operations.
The signing-only CLI fixture joins active stream sessions during shutdown.
Real loopback Chrome also produces signed/encrypted bytes and verifies discovery,
delivery/replies/replay, scoped reads, retained/live streaming, signed-cursor resume and
changed-byte tamper rejection, with independent native message observation.
Other browser engines and remote browser TLS remain unverified.

```sh
go test -race ./internal/gateway/... ./integration/gateway \
  -run 'Detached|DispatchAuthenticated|HTTPExactOrigins' -count=1
```
