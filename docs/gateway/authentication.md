# Gateway bearer credentials and permissions

## Authors (IRC style)

The gateway is not an identity authority. A plain request names its author
(see [contract-v1.md](contract-v1.md#authors)): a nickname alone is an AIRC
guest, a nickname with an account token is that account, and AIRC itself
authenticates it with SASL on the gateway's per-job session. Clients need no
TLS and no gateway credential; a listener may require an access password
(IRC's `PASS`). Tokens are accepted on any carrier and can be observed on
plaintext ones, so use TLS where the network is untrusted.

Tokens never reach disk. The journal keeps only
SHA-256(`airc-gateway-account-v1` NUL folded-nick token) to match a
resubmission; after a restart a registered author's unfinished job waits in
`credential_wait` until the same request ID returns with a matching token.
Connection carriers keep an identified author in memory for that connection
only. Abuse limits are IRC's: per-listener request rates and connection caps
(see [configuration.md](configuration.md#upstream-and-author-access)), and
aircd's own per-address limits on the real client, which the gateway forwards
with `WEBIRC` using the upstream's `gateway_token_file`: eight account
registrations per source an hour (`437`, reported as `capacity_exceeded`),
and nine failed tokens per source in ten minutes, after which aircd refuses
that source's tokens (`authentication_failed`) until the window passes.
Carriers without addresses share one source, `listener:<id>`.

The principal model below is the earlier, parallel identity system. It keeps
working until it is removed; the rest of this document describes it.

The bearer-verification primitive and local credential provisioning are
implemented. They are not yet wired to an ingress listener or delivery journal.
Signed/encrypted codecs are described in [signed-message-v1.md](signed-message-v1.md).
Store replay/revocation/grant APIs are implemented separately; their runtime
integration and transport-level authentication acceptance remain pending.
The complete catalogue remains outstanding. Additional AIRC operations are
optional under [ROADMAP.md](ROADMAP.md).

## Provisioning

On the verified macOS host, provision an external gateway credential into an
existing directory owned by the current effective user with mode 0700:

```sh
go run ./cmd/airc-gateway credential init --file /ABSOLUTE/PRIVATE/DIRECTORY/gateway.token
```

The path is a placeholder; do not use a live AIRC account or operator-token path.
The command creates only that new file, using 32 bytes from `crypto/rand`, hex
encodes them with one trailing newline, sets mode 0600, syncs and closes the file,
then syncs its parent directory. It prints only a creation notice. Existing files
and symlinks are never overwritten. Missing directories are not created. If
directory sync fails after the file is synced, failure is reported and the new
file is retained; inspect it locally rather than expecting a second init to
overwrite it. This does not create an AIRC account, a gateway principal record,
or a listener. Configure the file as one principal's `bearer_token_file`.

Keep gateway bearer, AIRC account/access, and AIRC operator credentials separate.
The runtime does not need an operator credential. New account provisioning on
the live daemon is not authorized by this command or by these examples.

## File boundary and verification

The Unix reader opens an owner-only parent directory, retains its descriptor,
and opens the final component relative to it with `O_NOFOLLOW`, `O_NONBLOCK`,
and close-on-exec. Descriptor metadata must show a regular file owned by the
current effective user with no group/other permission bits. Parent ownership
and permissions are also verified on its descriptor. Final-component symlinks,
symlinked immediate parents, directories, and FIFOs are rejected before reading.
Reads are bounded to 66 bytes; the accepted file contains exactly 64 hexadecimal
characters and optionally one newline. Hex case denotes the same key bytes.
Errors do not include credential contents. No unverified body can choose the
credential file or a principal ID.

`auth.New` snapshots validated principal/binding configuration, reads bearer
credentials through this boundary, and retains SHA-256 verifiers rather than
plaintext tokens. Duplicate keys across principals fail startup, including
different hex capitalization or different files holding the same key. Revoked
principals cannot authenticate. Every candidate compares against every loaded
verifier using `crypto/subtle.ConstantTimeCompare`; a matching entry's position
does not shorten the scan. Bad credentials return `authentication_failed`
without reflecting their input.

An authenticated `Identity` exposes its stable principal ID, backend binding,
and a copy of its operation set. Permission data is immutable. Authorization
normalizes a copied request, checks exact operation and typed target allowlists,
and defaults to deny. Room case remains significant; direct nickname case does
not. Modifying the source config or returned operation list cannot change the
snapshot. An unverified/zero identity never authorizes a request.

This check is one layer of authorization. Receipt reads still require a
principal-scoped journal key; replies still require retained-parent room/DM
authorization; history and live records still require participant filtering.
No actual backend request can yet be made through this primitive because the
dispatcher and adapters are unfinished. Their real rejection/no-post tests are
required later; primitive tests do not replace those acceptance checks.

## Rotation and revocation

The initial reload model is restart. Create a distinct new credential file and
point the same configured principal/binding to it. Building a new verifier
rejects the old key, accepts the new one, and preserves the stable upstream
operation ID. Set `revoked: true` to reject that principal in the next snapshot.
An already created snapshot remains immutable; these functions do not implement
live revocation, interrupt in-flight requests, or grant a dispatch exception.
The store now implements [durable revocation and one-attempt grants](dispatch-grants.md).
The dispatcher/backend must integrate them before runtime delivery is complete.

## Evidence and platform scope

Tests use disposable private directories and cover generated-file mode/size,
no overwrite, missing parent, malformed/oversized tokens, unsafe permissions,
symlinks, FIFOs, directory rejection, exact target/operation permissions,
immutable snapshots, duplicate keys, rotation, and configured revocation:

```sh
go test -race -count=1 ./internal/gateway/auth ./cmd/airc-gateway
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/...
```

A real `go run ... credential init` process created a fixture credential, and
a second process rejected overwrite. No secret was printed. All fixture files
and cross-build outputs were owned temporary resources and were removed after
the child processes exited. The live AIRC daemon/accounts/state were not used.

Native credential behavior is verified on darwin/arm64 with Go 1.27.1. Linux
amd64 and Windows amd64 gateway binaries cross-compile; that is build evidence
only. Unix permission/ownership APIs are implemented; non-Unix credential
operations explicitly return an unavailable-platform error until native ACL
verification is implemented and tested. No Windows credential or native IPC
support is claimed. No privileged cross-UID fixture has been run; descriptor
owner checks remain part of the code review, while cross-user runtime acceptance
must be established on a suitable isolated host.
