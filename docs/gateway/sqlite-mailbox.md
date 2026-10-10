# SQLite database mailbox

The selected database mailbox is **SQLite 3**, using the repository's existing
`modernc.org/sqlite` driver. Each owner processes one slot in an explicitly
provisioned local `mailbox.sqlite`. Requests carry encrypted author-signed AIRC
intents; results are signed and encrypted for a fixed recipient. Darwin and
Linux are the implementation targets. Testing and deployment are deferred.

## Configuration and provisioning

Add a top-level `sqlite_mailboxes` array with at most 16 owners:

```json
{
  "sqlite_mailboxes": [{
    "id": "alice-database",
    "enabled": true,
    "directory": "/private/gateway-mailboxes/alice",
    "route": {
      "principal": "alice",
      "target": {"kind": "room", "name": "#general"},
      "operations": ["send", "context.read", "history.read", "search"],
      "recipient_key_id": "alice-results",
      "recipient_public_key_file": "/private/gateway/alice-results.pem"
    }
  }]
}
```

Replace the path and identity placeholders. The principal needs a signing key
and the exact target/operation grants. Configure the existing request-decryption
keys, Ed25519 response signer and P-256 response recipient. History and search
also require the cursor key. The directory must be separate from gateway state,
keys and other local carriers. Restart after changing provisioning.

Provision an existing owner-only directory (`0700`) and a regular single-link
database (`0600`) owned by the gateway UID, on a local filesystem with reliable
SQLite locking. The producer runs under that same trusted administrative UID.
That UID controls the filesystem and database; the signed intent independently
establishes the native AIRC author and grant. An untrusted SQL account or network
filesystem is outside this profile. Do not replace the database or directory
while either participant has it open.

The configured path must match its resolved absolute path. Ancestors must be
owned by root or the gateway UID and must not be group/world writable, except
root-owned sticky temporary directories. Linux accepts ext4, XFS and Btrfs;
Darwin requires the filesystem's local flag. Database and directory filesystems
are checked separately.

Use the supplied [schema](sqlite-mailbox.sql) to provision a new empty database
before starting the gateway. The gateway opens in read/write-existing mode and
never initializes, repairs or migrates the mailbox. The exact schema, application
ID and version are a cross-process contract. Additional tables, indexes,
triggers and views are refused. Preserve the schema's statement spelling: its
stored SQL is checked exactly. Schema changes require a stopped, coordinated
client migration.

## Transaction and producer contract

The database contains one `mailbox` table. Slot `1` holds the whole compact JWE
as a BLOB of at most 16 KiB; `result` starts as SQL NULL. Insert only when the
table is empty, using bound parameters:

```sql
INSERT INTO mailbox(slot, request, result) VALUES (1, ?, NULL);
```

The producer commits that insert and leaves the row unchanged. The gateway
acquires a [SQLite immediate write transaction](https://www.sqlite.org/lang_transaction.html),
checks the schema and pending row, then holds the transaction through native
admission and publication of a whole protected result BLOB of at most 64 KiB.
An unread non-NULL result prevents further admission. The gateway updates only
the exact request still in slot 1 with a NULL result; it never deletes rows.

The producer polls with short read transactions. After verifying the result's
signature, recipient, scope, validity, request ciphertext digest and native
request ID where present, consume it with a conditional delete:

```sql
DELETE FROM mailbox WHERE slot=1 AND request=? AND result=?;
```

Bind the exact request and result BLOBs that were verified and require one
affected row. Do not submit another request until that delete commits. Do not
hold a read transaction while waiting for a result: it can prevent the gateway
from committing. Multiple producers must coordinate use of this single slot.

Run one gateway owner for a mailbox and preserve its durable gateway journal
across restarts. A lifetime directory lock excludes another gateway owner;
producers use SQLite transactions and do not take that gateway lock. Never
recover a pending mailbox using an empty replacement gateway journal.

Native journal admission and the mailbox transaction are separate commits. A
crash or uncertain commit can leave a request pending after AIRC admission.
Retry retains the original signed validity and native request ID; an identical
retained Send recovers its durable result under current grants. Redelivery does
not authorize a new Send or renew freshness. Reads can repeat and require fresh
intent. A result containing a transient or uncertain native error is still a
complete result; the producer must reconcile it before deciding to resubmit.

Malformed or unauthorized requests receive an encrypted refusal for the fixed
recipient. A response that exceeds whole-payload capacity becomes an encrypted
capacity error, without publishing a prefix. A stale or corrupt result requires
coordinated recovery after stopping the gateway and reconciling the outstanding
intent; the gateway does not automatically clear an unread result.

## Protection, bounds and ownership

`SQLiteMailboxResult` contains version 1, protocol `sqlite/mailbox`,
`request_sha256`, optional authenticated `request_id`, target, and exactly one
whole `result` or `error`. It uses the existing Ed25519 JWS inside
P-256 ECDH-ES/A256GCM JWE profile, with 32 KiB clear payload, 64 KiB token and
120-second lifetime limits. The response scope contains realm, principal and
binding. Its route is lowercase SHA-256 hex of the following values joined
with NUL, without a trailing separator:

```text
airc-sqlite-mailbox-response-v1, owner-id, directory, target-kind, target-name
```

`SQLiteMailboxResponseScope` constructs it. Mailbox publication is not native
delivery confirmation; inspect the protected durable Send state and existing
receipt mechanisms.

Each owner has one connection and polls serially with a five-second pass budget,
three-second native admission budget, one-second idle delay and error backoff
capped at 15 seconds. Shared request capacity covers the transaction, admission
and publication. The database is bounded to 8 MiB. Only rollback-journal
`DELETE` mode is accepted; the gateway requires full synchronous writes, disables
trusted schema expressions and bounds lock waits and SQL resources. Required
[PRAGMA settings](https://www.sqlite.org/pragma.html) are read back. No SQL from
request content is executed and no extension is loaded.

A private regular rollback journal of at most 16 MiB is allowed, including for
SQLite's crash recovery; WAL and shared-memory sidecars are refused. A busy
commit is rolled back and retried on a later poll. Failed rollback or changed
file identity stops the owner rather than reusing an uncertain transaction.

Cancellation rolls back incomplete work. Service shutdown joins owners before
closing databases and clearing response signing keys. A readiness record means
local preparation and worker launch, not a completed mailbox pass or native
AIRC acceptance. This profile
adds a separate mailbox schema without changing the gateway journal schema or
adding a dependency.

## Deferred verification

Production `go build ./...` passes for Darwin ARM64 and Linux AMD64/ARM64 with
`CGO_ENABLED=0`. Source review, formatting, whitespace and source-size advisory
checks pass. These checks do not establish runtime acceptance.

Runtime interoperability with an independent SQLite producer, process/crash and
busy-lock recovery, schema/ownership refusals, exact native author/target/body,
whole encrypted reads and cursor boundaries, replay, capacity and joined
shutdown remain unexecuted by owner direction. Compilation does not qualify
these behaviors.
