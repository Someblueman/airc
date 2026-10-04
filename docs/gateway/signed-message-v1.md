# Gateway signed and encrypted message profiles v1

The codecs and provisioned-key verification are implemented as Go APIs. No
listener or complete authentication/dispatch pipeline is implemented yet. The
separate [journal intake API](journal-v1.md) can atomically record job/replay
identity, but no ingress pipeline connects it to these codecs yet. These APIs
return verified intent, not an accepted delivery.
The catalogue and all supported operations remain required.

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
fingerprints and records replay markers. Runtime integration and pruning must
retain those markers through this deadline. Calling a codec
twice does not claim two admissions or enforce replay persistence. Key rotation
must not reset this identity scope. The store's [durable revocation/grant APIs](dispatch-grants.md)
still need runtime integration. Ingress rate/concurrency limits, parent/read filtering, and real
transport rejection/no-post tests remain mandatory before runtime acceptance.

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
