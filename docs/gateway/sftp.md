# OpenSSH SFTP mailbox

The chosen transfer service is an existing OpenSSH server exposing its SFTP v3
subsystem and `fsync@openssh.com` extension. The gateway polls one dedicated
account mailbox over a pinned SSH connection, accepts signed-and-encrypted
requests, and publishes complete encrypted Send or read results. Server/account
provisioning is a deployment input; the gateway does not install or configure
OpenSSH. Runtime and native AIRC qualification are deferred.

OpenSSH documents its v3 wire profile and file-sync extension in
[PROTOCOL](https://github.com/openssh/openssh-portable/blob/master/PROTOCOL),
using the [SFTP v3 draft](https://www.openssh.com/txt/draft-ietf-secsh-filexfer-02.txt).
An operator can restrict the account to `internal-sftp` with forwarding disabled
and an appropriately provisioned chroot; these are existing OpenSSH settings,
described by [sshd_config](https://man.openbsd.org/sshd_config.5).

## Provisioning and authority

The `sftp` collection accepts at most 16 explicit owners. Each has a distinct ID,
canonical numeric unicast IP/port, account name, local SSH private-key and pinned
host-public-key files, canonical remote root, nonzero remote UID, and one fixed
AIRC principal/target/operation route. Remote roots on the same endpoint cannot
overlap within a configuration. An enabled SFTP owner cannot accompany a command
that requires an exclusive listener, such as `import-bundle` or `serve-stdio`.

The SSH client and host keys are Ed25519. The local private key is an unencrypted
OpenSSH or PKCS#8 PEM in an owner-private regular file; the host key file contains
exactly one `ssh-ed25519` public-key entry, optionally with a comment. SSH
certificates, key options, ambient agents/passwords, interactive prompts, proxies,
shell commands and forwarding are outside this profile. The existing cached
`golang.org/x/crypto/ssh` implementation performs SSH authentication and encryption.

The remote root must already be a mode-0700 directory owned by `remote_uid`.
Request/result files must be regular mode-0600 files with that UID. LSTAT rejects
final symlinks; FSTAT validates the actual opened input before and after reading.
The pinned server remains trusted for account isolation, ancestor paths, UID,
mode and file-operation semantics. SFTP v3 does not provide local inode/ACL or
descriptor-relative path proofs. Use one gateway owner and one producer per
mailbox; multiple independent configurations are not a distributed queue.

SSH access alone cannot choose the AIRC identity. Every request is the existing
compact JWE containing the author's signed intent. The service requires gateway
ingress recipient keys, an active principal signing key, an exact authorized
target, explicit operations and a gateway response signer. Accepted operations
are `send`, `context.read`, `history.read` and `search`; history/search require a
cursor key. The verified principal and binding must match the configured route.
Native reads use current durable grants and the existing signed validity bound.

This fragment uses illustrative values; supply actual account, UID, keys and
existing principal/target grants before enabling it:

```json
{
  "sftp": [{
    "id": "device-transfer",
    "enabled": false,
    "address": "192.0.2.10:22",
    "username": "airc_device",
    "private_key_file": "/private/airc/device-ssh-key",
    "host_key_file": "/private/airc/server-ed25519.pub",
    "root": "/mailbox",
    "remote_uid": 1001,
    "route": {
      "principal": "device",
      "target": {"kind": "room", "name": "lobby"},
      "operations": ["send", "history.read"],
      "recipient_key_id": "device-replies",
      "recipient_public_key_file": "/private/airc/device-response.pem"
    }
  }]
}
```

The remote path is relative to the SFTP account's visible filesystem (including
its chroot). It is not a local gateway path. Readiness records a configured
owner; it does not certify the remote server or native AIRC availability.

## Single outstanding request

The fixed mailbox files are `request.jwe`, `result.tmp` and `result.jwe`.
The producer uploads the complete 1–16,384-byte compact request without newline
or JSON wrapping to a separate temporary name, syncs it, then atomically renames
it to `request.jwe`. It must keep that request immutable until its matching
result is consumed. Only the gateway may write the result names. The producer
is responsible for storage quotas and for its own temporary upload files.

An existing `result.jwe` provides backpressure and is never overwritten. Otherwise
the gateway reads the complete bounded request, authenticates and dispatches it,
then exclusively creates `result.tmp`, writes the whole encrypted result,
requests file fsync, closes the handle and uses standard nonoverwriting SFTP v3
RENAME to publish `result.jwe`. No directory scan, wildcard path, arbitrary
download, streaming result or fragment assembly occurs.

The gateway never deletes `request.jwe`, even after successful publication.
After verifying a result and matching its protected digest to the exact upload,
the producer removes `request.jwe` first, then `result.jwe`, before publishing
the next request. This order prevents the consumed request from being treated
as another pending upload. Losing a reply does not authorize a new native Send:
retry uses the same signed request ID and existing durable journal binding.
An expired envelope can only recover an already-retained identical Send under
current authority; it cannot trigger fresh execution. There is no new schema.

If an interrupted write or concurrent owner leaves `result.tmp`, the gateway
refuses to replace it. It neither guesses ownership from timestamps nor renames
an unverified leftover into a result. Stop all mailbox owners before an operator
removes or quarantines that temporary file, preserve the original request, then
restart to replay it. This deliberate recovery boundary prevents one writer
from publishing another writer's partial bytes. A rename acknowledgement can be
lost after publication; the next pass preserves any completed result. The file
is synced before publication, but SFTP does not establish directory fsync or
server power-loss durability. Input retention is the recovery source.

## Whole encrypted results and bounds

`SFTPResult` has version 1, protocol `sftp`, `request_sha256` (lowercase SHA-256 of
the exact uploaded ciphertext), fixed target, and either `result` or `error`.
Authenticated requests also carry their signed `request_id`; an authentication
refusal supplies no trusted request ID. The protected digest is required when
correlating results, because file names and SSH metadata are not signed intent.
Only a standard durable Send result establishes gateway responsibility; an
SFTP transfer success does not establish native acceptance.

Results use the existing Ed25519 signature inside P-256/A256GCM JWE, with a
32-KiB clear-payload limit, 64-KiB compact-token limit and 120-second lifetime.
`SFTPResponseScope` includes realm, principal, binding and a SHA-256 route digest
over NUL-separated `airc-sftp-response-v1`, owner ID, exact configured address,
username, root, decimal remote UID, target kind and target name. The configured
recipient is fixed for the mailbox. Clients verify the scope, signer, expiry,
digest and request ID before acting on a result.

Read replies retain the whole standard page/context and cursor boundary. An
oversized result is replaced with a complete encrypted capacity failure; pages
are not silently shortened or split. Authentication/authorization failures also
produce a bounded encrypted refusal for the configured recipient, without
executing the request. A client finding an expired reply can remove only that
result while preserving the exact request, allowing authenticated retry or an
explicit expiry refusal; a fresh read requires a fresh signed intent.

Each poll owns one connection for at most 15 seconds, including SSH setup and
all SFTP work. File processing shares the global request capacity and native
work is capped at three seconds. Idle polls wait one second; errors back off to
15 seconds. Packets are bounded to 64 KiB and file I/O chunks to 32 KiB. Shutdown
closes the transport, joins owned workers and service owners, then clears SSH
and response-signing keys. Production compilation is separate from deferred
OpenSSH interoperability, permission/race/crash boundaries, replay, complete
read pages and independent native AIRC delivery testing.

Production builds passed for Darwin ARM64 and Linux AMD64/ARM64 with CGO
disabled. No SFTP server was contacted and no runtime tests were run.
