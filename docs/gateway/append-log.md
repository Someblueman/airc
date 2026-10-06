# Append-only log ingestion

The bounded filesystem reader and retained claim, result and cursor primitives
and signed/encrypted owner-restricted dispatch are implemented for Darwin and
Linux. Principal-owned service polling and joined shutdown are implemented.
Native qualification remains on the roadmap. Reader cursor proposals alone do
not prove journal intake or delivery.

An existing mode-0700 owner-private root contains an existing mode-0700 `logs`
directory. The reader reuses the mailbox root's normalized absolute path,
trusted-ancestor, no-symlink, owner-UID, opened-inode and exclusive directory-flock
boundary. It rechecks root and logs privacy on each call. Calls are sequential;
the caller closes the owned reader. No source file is renamed, removed or
rewritten by this reader.

Generations are positive unsigned 64-bit integers, starting at 1 and advancing
one at a time. Filenames contain the generation as exactly 20 zero-padded decimal
digits followed by `.jsonl`, for example `00000000000000000001.jsonl`. A directory
scan reads at most 129 entries and refuses more than 128 or a noncanonical name.
Each selected source must be a mode-0600 current-UID single-link regular file;
opens use nonblocking/no-follow flags so FIFO and symlink sources are refused.
Each generation file contains at most 8,388,608 bytes. These bounds constrain
gateway reads and accepted files; the producer still owns its filesystem storage
budget and can create arbitrary files outside this protocol.

A nonempty record consists of at most 16,384 opaque bytes followed by a newline.
The newline is not part of the returned body. Producer JSON must keep newlines
inside strings escaped. An empty line seals a generation and must be its final
byte. A producer appends to the same inode, syncs completed records, seals and
syncs the generation, and atomically publishes the next generation before
retiring an acknowledged sealed source. It must not overwrite existing bytes,
reuse generation numbers or delete an unacknowledged generation. Content and
generation metadata do not authenticate an AIRC principal at this boundary.

`Next` accepts an explicit cursor containing generation, byte offset, device,
inode and sealed state. A new reader starts with generation 1 and zero offset
and identity. Each successful record proposes the boundary immediately after
its newline and the actual opened file identity. A sealed cursor selects the
next generation only; gaps fail closed. A retained sealed source must still have
the recorded identity and exact end size; deletion is allowed only after its
seal has been acknowledged by the durable mailbox owner. An unsealed source
cannot disappear, change inode or shrink below its recorded offset. The reader
does not detect all same-inode overwrites of earlier bytes; append-only producer
behavior remains a protocol requirement. It rechecks selected file privacy,
size and named/opened inode agreement before returning a complete record.

Each record read allocates at most 16,386 bytes. An incomplete record of up to
16,384 bytes returns `ErrAppendLogIncomplete` without a new cursor; the caller
must retry its unchanged cursor after more bytes arrive. Empty EOF similarly
returns no proposal. A record exceeding the body bound is refused immediately,
even without its newline. No buffer grows across partial reads. Complete records
are synced before return. The reader proposes boundaries; `AppendLogMailbox`
retains them before dispatch and checkpoints them after publication.

The mailbox owns the same exclusive root and three fixed progress files:
`claim.json`, `response.json` and `cursor.json`. All must be mode-0600,
current-UID, single-link regular files. State reads are bounded and reject unknown
JSON fields, changed inode/size, symlinks and FIFOs. A version-1 claim retains the
before and next cursors and canonical padded base64 body, including opaque
non-UTF-8 input. Claims are bounded to 32,768 bytes and checkpoints to 1,024.
Each slot has one fixed temporary file; stale unsafe temporaries fail closed.
Temporary files never constitute completed state. Recovery recognizes only the
exact two-name, two-link same-inode `claim.json`/`claim.json.tmp` pair left by
interrupted exclusive publication: it syncs the completed name, removes the
temporary alias and syncs the root before reading the claim. Other hard links
remain refused.

`Claim` syncs and exclusively publishes the retained input before returning its
stored bytes. Reopening returns that snapshot even if the source disappeared.
An unread response blocks further claims. `Publish` accepts a nonempty result of
at most 65,536 bytes, syncs and exclusively publishes it, then syncs and atomically
replaces the cursor, then removes the claim. Each publication/removal syncs the
root directory. An empty generation seal requires an empty publication and
advances its cursor without producing a response. Source logs remain unchanged.

If publication survives without its checkpoint, the unread response blocks
recovery until consumed, then the retained claim replays. If the checkpoint
survives with the claim, recovery cleans up that completed claim and selects the
next record. Replay may therefore repeat dispatch; the future authenticated
owner must use the existing journal's stable request identity. These progress
files are gateway-owned and must not be mutated by the producer. The filesystem primitive
does not authenticate, admit or deliver messages by itself.

`ProcessAppendLogMailbox` accepts bounded version-1 correlated frames using the
existing signed or encrypted envelope verifier. It compares the verified
principal and binding to the provisioned owner before durable intake. Streaming
and other profiles are refused. Responses preserve frame correlation; the log
position is a transport boundary, while authenticated request identity controls
journal replay. Cancellation retains the claim. Seals checkpoint without
dispatch. The `serve` command enables this connector through explicit listener
configuration.

Actual filesystem tests cover an appended partial record, cursor JSON roundtrip
and reader reopening, seal and generation rotation, retirement of an acknowledged
sealed source, missing next-generation EOF, replacement/truncation, missing
unsealed sources, generation gaps, bytes after seal, exact record/partial bounds,
complete and incomplete overflow, exact generation size, file-size overflow,
symlinks, hard links, public files, directories, nonblocking FIFO refusal,
noncanonical names, generation quota, invalid cursor and changed logs privacy.
Additional controls cover retained claims across reopening and source loss,
interrupted claim-link publication, result-before-checkpoint replay,
checkpoint-before-cleanup recovery, exact opaque
16,384-byte claims, exact 65,536-byte results, oversized/empty result refusal,
seal checkpointing and rotation, exclusive ownership, invalid versions/cursors,
unknown fields, noncanonical base64 and unsafe claim/state temporary files. The
publication-gap controls create real filesystem states and reopen the owner;
they do not kill a process during publication or prove power-loss behavior.
The complete adapter package passed under race detection on Darwin (16.134
seconds), with adapter vet/staticcheck, formatting and diff checks. This evidence
does not establish native AIRC acceptance, service
shutdown/crash recovery or Linux runtime behavior.

Authenticated dispatch controls passed under race detection (3.790 seconds),
covering durable owned intake, repeated signed request identity, wrong owner or
binding, invalid envelope/profile/version/correlation, streaming and malformed
frames, cancellation retention and seals without journal intake. These controls
use the real envelope verifier and SQLite journal; they do not prove native
AIRC delivery or the encrypted path's native acceptance.

A listener uses `"protocol":"append_log"`, a normalized absolute private root
as `address`, `"append_log":{"principal":"owner-id"}`, and explicit `send`,
`receipt.get` or `history.read` operations. Its owner must be active with a signing
key and an existing binding. TLS and origins are unsupported. Configuration
refuses roots overlapping journal state, credentials, another mailbox or local
listener/backend sockets, including aliases through existing ancestors. Root
and `logs` must already exist; the service's owned open enforces owner UID and
private filesystem boundaries. Enabled listeners are Darwin/Linux only and
discovery reports `enabled_append_log_partial`.

The sequential poller shares request admission with other listeners. EOF, a
bounded partial record or an unread result triggers a fixed 100-millisecond idle
interval. A missing provisioned directory or unsafe source/state fails the
service. Cancellation joins pollers and delivery before closing mailbox roots
and journal ownership; startup failures release opened owners before readiness.
Affected configuration/service/CLI packages passed under race detection (1.347,
6.122 and 2.769 seconds), with build, vet and staticcheck. Real filesystem service
controls cover partial records completing after polling, authentication refusals,
exclusive ownership, checkpointed restart, joined shutdown, missing-directory
failure and startup failure cleanup. Native signed/encrypted acceptance, reading,
rotation/replay/crash qualification and Linux runtime remain unverified.
