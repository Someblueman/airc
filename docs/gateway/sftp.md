# OpenSSH SFTP mailbox

The chosen transfer service is an existing OpenSSH server exposing its SFTP v3
subsystem and `fsync@openssh.com` extension. The gateway polls one dedicated
account mailbox over a pinned SSH connection, accepts plain contract requests,
and publishes complete plain Send or read results. Server/account
provisioning is a deployment input; the gateway does not install or configure
OpenSSH. Local OpenSSH evidence is at the end; deployment is untested.

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
AIRC target/operation route with its author policy. Remote roots on the same endpoint cannot
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

The SSH account and the mailbox's remote owner are the access gate. A request
is a plain contract request and posts as the author it names (a registered
account with its token always; a guest only when the route's `guests` allows),
else as the route's `bound_author`; see [route authors](route-authors.md). The
forwarded client source is `route:<id>`. Accepted operations are `send`,
`context.read`, `history.read` and `search`; history/search require a cursor
key. A request for another target or an unlisted operation gets a `forbidden`
result without dispatch. There are no recipient, signing or response keys:
anyone who can write the mailbox can post as its bound author, and a token in a
request is readable by the SSH server and anyone with access to the account.

This fragment uses illustrative values; supply the actual account, UID and keys
before enabling it:

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
      "bound_author": {"nick": "device", "account_token_file": "/private/airc/device.token"},
      "target": {"kind": "room", "name": "lobby"},
      "operations": ["send", "history.read"]
    }
  }]
}
```

The remote path is relative to the SFTP account's visible filesystem (including
its chroot). It is not a local gateway path. Readiness records a configured
owner; it does not certify the remote server or native AIRC availability.

## Single outstanding request

The fixed mailbox files are `request.json`, `result.tmp` and `result.json`.
The producer uploads the complete 1–16,384-byte JSON request to a separate
temporary name, syncs it, then atomically renames
it to `request.json`. It must keep that request immutable until its matching
result is consumed. Only the gateway may write the result names. The producer
is responsible for storage quotas and for its own temporary upload files.

An existing `result.json` provides backpressure and is never overwritten. Otherwise
the gateway reads the complete bounded request, authenticates and dispatches it,
then exclusively creates `result.tmp`, writes the whole result,
requests file fsync, closes the handle and uses standard nonoverwriting SFTP v3
RENAME to publish `result.json`. No directory scan, wildcard path, arbitrary
download, streaming result or fragment assembly occurs.

The gateway never deletes `request.json`, even after successful publication.
After matching a result's digest to the exact upload,
the producer removes `request.json` first, then `result.json`, before publishing
the next request. This order prevents the consumed request from being treated
as another pending upload. Losing a reply does not authorize a new native Send:
retry uses the same author and request ID, which the journal deduplicates.

If an interrupted write or concurrent owner leaves `result.tmp`, the gateway
refuses to replace it. It neither guesses ownership from timestamps nor renames
an unverified leftover into a result. Stop all mailbox owners before an operator
removes or quarantines that temporary file, preserve the original request, then
restart to replay it. This deliberate recovery boundary prevents one writer
from publishing another writer's partial bytes. A rename acknowledgement can be
lost after publication; the next pass preserves any completed result. The file
is synced before publication, but SFTP does not establish directory fsync or
server power-loss durability. Input retention is the recovery source.

## Whole results and bounds

`SFTPResult` has version 1, protocol `sftp`, `request_sha256` (lowercase SHA-256 of
the exact uploaded ciphertext), fixed target, and either `result` or `error`.
Decodable requests also carry their `request_id`; an undecodable one gets
`invalid_request` without one. Correlate by the digest: file names and SSH
metadata are not part of the request.
Only a standard durable Send result establishes gateway responsibility; an
SFTP transfer success does not establish native acceptance.

Results are plain JSON of at most 64 KiB. Earlier checkpoints signed and
encrypted results to a configured recipient; the author model removed that
envelope, so the SSH channel and the mailbox's file ownership are the only
protection of results in transit and at rest.

Read replies retain the whole standard page/context and cursor boundary. An
oversized result is replaced with a complete capacity failure; pages
are not silently shortened or split. Refusals also produce a bounded result
without executing the request.

Each poll owns one connection for at most 15 seconds, including SSH setup and
all SFTP work. File processing shares the global request capacity and native
work is capped at three seconds. Idle polls wait one second; errors back off to
15 seconds. Packets are bounded to 64 KiB and file I/O chunks to 32 KiB. Shutdown
closes the transport, joins owned workers and service owners, then clears SSH
keys.

At the original checkpoint, production builds passed for Darwin ARM64 and
Linux AMD64/ARM64 with CGO disabled; no SFTP server was contacted.

On 2026-10-11 a local OpenSSH 9.6 server (`internal-sftp`, unprivileged
account) held the mailbox and the OpenSSH `sftp` client acted as producer. A
request without an author posted as the registered bound author, one naming a
guest posted as that guest, another room got a `forbidden` result and a
`history.read` returned a page; an independent `airc history` client saw both
posts. Remote servers, crash and permission races were not exercised.
