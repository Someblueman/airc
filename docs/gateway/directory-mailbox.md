# Watched-directory mailbox

The bounded named-file filesystem boundary is implemented for Darwin and Linux.
Authenticated frame dispatch and configured polling with an explicit
principal/binding owner are implemented. Darwin signed/encrypted native AIRC
acceptance, complete bounded cursor pages and clean restart replay are verified.
Owner isolation/refusal, quota, process-crash and Linux runtime qualification
remain on the roadmap. This is an enabled service profile with partial
qualification, available through the ordinary `airc-gateway serve` command.

An existing owner-private root contains three existing mode-0700 directories:
`inbox`, `processing`, and `results`. The root has the same trusted-ancestor,
owner-UID, normalized absolute path, opened-inode, and exclusive directory-flock
checks as the [one-file mailbox](file-mailbox.md). Subdirectory ownership and
privacy are rechecked before every claim and publication; symlink directories
are refused. Calls are sequential and the caller owns and closes the boundary.

Producers atomically publish complete mode-0600 single-link regular files in
`inbox`. A filename consists of 1 through 64 ASCII letters, digits, hyphens or
underscores followed by `.json`. Inputs contain 1 through 16,384 bytes. The
producer must sync the complete file before renaming it into the inbox and sync
the inbox directory after publication. Temporary producer files must remain
outside the three scanned directories until complete. File presence and ownership
do not authenticate an AIRC principal; content is opaque at the filesystem boundary.

The frame dispatcher accepts version-1 signed or encrypted detached envelopes,
verifies the configured audience, signing identity and freshness through the
shared authenticator, then compares both verified principal and binding with the
explicit directory owner before dispatch. It refuses streaming and other
profiles. The frame correlation ID must equal the filename stem; this transport
ID remains separate from the envelope's durable request ID. A bounded correlated
result or refusal is published under the claimed filename. Cancellation retains
the processing input instead of publishing a result. Durable journal replay uses
the existing stable upstream identity and authorization boundary. Directory
ownership alone cannot select or impersonate an AIRC account.

A listener requires explicit `send`, `receipt_get` and/or `history_read`
operations and a `directory_mailbox` option naming an existing active principal
with a signing key. The principal's configured binding fixes the AIRC account.
For example, within an otherwise provisioned gateway configuration:

```json
{
  "id": "device-inbox",
  "protocol": "directory_mailbox",
  "address": "/private/device-inbox",
  "enabled": true,
  "operations": ["send", "receipt_get", "history_read"],
  "directory_mailbox": {"principal": "device"}
}
```

The directories must already exist with private ownership; startup does not
provision them. Mailbox roots cannot overlap each other, journal state,
credentials, or local listener/backend socket paths. Existing ancestor aliases
are resolved even for future state paths. TLS/origins and implicit operations
are refused. Other enabled listeners can share the ordinary gateway service,
subject to their existing configuration restrictions.

Each configured directory has one sequential poller sharing the service request
limit. Empty queues and unread-result backpressure wait 100 milliseconds before
another bounded scan. Successful publication permits immediate progress to the
next name. Invalid filesystem state stops the service; a missing required
subdirectory is not an empty queue. Producers replacing a file concurrently may
cause validation refusal and must handle a stopped service conservatively.
Authenticated content refusals publish a named result and allow later names to
proceed. Shutdown cancels and joins pollers and delivery workers before closing
mailbox roots or journal ownership; failed startup releases earlier roots
without emitting readiness.

At most 128 inbox entries and 128 result entries are accepted, with one claimed
processing file. Each scan reads only the quota plus one entry, then sorts the
bounded list. Over-quota directories or unexpected names fail closed without
consuming producer inputs. FIFO, symlink, hard-linked, public, oversized and
nonregular inputs use the existing nonblocking/no-follow refusal boundary.
These quotas bound gateway scans and admitted results; the producer must respect
its private filesystem storage budget. The gateway cannot prevent its OS owner
from creating arbitrary files outside this protocol.

A retained processing file takes priority over new inbox files. Otherwise claim
selects the first lexical name with no unread result of that name, renames it to
`processing`, and syncs the processing directory before the source inbox. Its
body is reread and synced from the retained processing file within the existing
input bound. This keeps returned bytes identical to the replay intent even when
a producer atomically replaces the inbox name during claim. Validation may
refuse an inode concurrently unlinked by a replacement before claim. Unread results for other
names do not prevent progress; a full results directory prevents both new and
resumed intake until a result slot is freed. Reusing a name requires consuming
its prior result. No new file replaces an interrupted claim.

Publication must match the single claimed name. It writes and syncs the complete
bounded result through the shared private temporary-file implementation, links
it exclusively into `results`, syncs that directory, then removes the claimed
input and syncs `processing`. Results contain 1 through 65,536 bytes. An existing
result is never overwritten. If interruption leaves both processing and result
files, the unread result blocks resume until consumed, then the retained intent
can be replayed through the authenticated journal boundary. A leftover
private temporary result is discarded and rewritten; it is not a completed
result. The shared one-file profile keeps its original fixed filenames and
publication behavior.

Tests cover lexical queue processing, same-name unread result preservation,
independent unread results, exact body/result limits, mismatch/path refusal,
exclusive ownership, close/reopen of a claimed file, a publication gap, quotas,
unsafe names/types, changed subdirectory permissions, and 100 successful claims
while a concurrent producer repeatedly atomically replaces the inbox name. Every
successful claim is compared with its retained processing bytes. This is filesystem
interruption evidence; it does not establish native AIRC
acceptance, daemon shutdown, actual process hard-kill, or Linux runtime behavior.

The full adapter package passed under race detection on Darwin after the retained
input correction and concurrent replacement control (14.490 seconds).
Adapter vet/staticcheck, formatting and diff checks passed. All three existing
actual one-file mailbox CLI controls also passed together under race detection
(package 37.748 seconds) after sharing the publication path, retaining signed and
encrypted native delivery, exact cursor pages, refusal/size controls and pending
process termination recovery. Gateway executable SHA-256:
`5b6c040ada73f71be4d2386d4dc5f48b3085034ffd0cbd3695471534a56f1225`.
This regression evidence does not qualify the new directory connector.

Authenticated dispatch controls use real Ed25519 envelopes and the SQLite
journal: signed intake retains the expected principal, binding and normalized
body; replay preserves the upstream ID; mismatched principal/binding, malformed
envelope, filename correlation mismatch, streaming and bearer profile produce
refusals without journal intake. Cancellation retains a previously claimed
input. The full adapter race suite passed after dispatch integration (17.092
seconds), with adapter vet/staticcheck and diff checks. These dispatcher controls
establish journal intake and refusals; native evidence is described below.

Service controls exercise actual polling, correlated authentication refusal,
exclusive directory ownership, two starts with joined shutdown and journal
reopen, startup bind failure cleanup, and removal of a required subdirectory
causing service failure followed by directory/journal reopen. On Darwin the
affected race suites passed: config 1.403 seconds, service 4.333 seconds,
adapter 15.771 seconds and CLI 2.593 seconds. The added missing-directory control
and final directory lifecycle tests passed under race detection (service package
3.362 seconds). CLI build, affected vet/staticcheck, formatting and diff checks
passed. Linux runtime behavior and process-crash evidence remain unfinished.

The actual directory service CLI control passed under race
detection on Darwin (test 12.86 seconds, package 14.542 seconds). A separately
observed AIRC instance accepted one signed and one encrypted request; each
receipt reported accepted with one send attempt, and its independent observer
matched author, target and complete multiline Unicode body. After a clean
SIGINT shutdown and fresh gateway process for each intent, replay kept the
accepted message ID and attempt count. Native CHECK contained exactly two
records. One-record history pages matched those records completely, with
explicit `more` then `ok` status, nonempty cursors, no gaps and a final empty
caught-up page. This establishes clean service restart replay, not hard-kill,
lost-acknowledgement or power-loss recovery. Isolation/refusal and quota native
controls remain separate acceptance work. Integration vet/staticcheck,
formatting and diff checks passed. The tested current gateway executable
SHA-256 was
`b986587b1d7f9d760908f789bd13b2457bb1de09791950b5025464cd883ea0be`;
the isolated native fixture baseline was
`96f16c3bbb8a3faac2e69684f591af63ceb58450`.
