# Watched-directory mailbox

The bounded named-file filesystem boundary is implemented for Darwin and Linux.
Authenticated frame dispatch with an explicit principal/binding owner is also
implemented. Polling, configured service ownership, and actual native
AIRC/crash qualification remain on the roadmap. This is not yet an enabled
listener or command.

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
can be replayed through the future authenticated journal boundary. A leftover
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
seconds), with adapter vet/staticcheck and diff checks. Encrypted native delivery,
bounded cursor reads, polling and joined daemon ownership remain on the roadmap;
these dispatch controls do not replace those acceptance requirements.
