# Append-only log ingestion

The bounded filesystem reader and retained claim, result and cursor primitives
and signed/encrypted owner-restricted dispatch are implemented for Darwin and
Linux. Principal-owned service polling and joined shutdown are implemented.
Darwin and Linux native delivery, complete bounded cursor reads, replay and
recovery have separate evidence below. Reader cursor proposals alone do not
prove journal intake or delivery. Remaining work lives in the roadmap.

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
failure and startup failure cleanup. These service controls do not establish native generation rotation, pending
abrupt termination, refusal/size recovery or Linux runtime qualification.

On Darwin, `TestAppendLogNativeAcceptanceRestartReplayAndBoundedHistory` passed
with race detection against the actual gateway child built from `dce443b`
(14.264 seconds). Signed and encrypted Unicode/newline sends each reached
`accepted` with one send attempt; an independent native observer checked the
exact author, room and body. Joined SIGINT shutdown and a fresh child restart
preserved accepted message IDs and attempt counts for both replayed requests.
Native CHECK returned exactly two records. History pages of one record matched
the complete native records, with explicit nonempty cursors, `more` then `ok`,
no gap, and a final empty caught-up page. This closes Darwin delivery, restart
replay and bounded reads. Separate controls are required for rotation, pending
abrupt termination, refusal/size recovery and Linux runtime qualification.

`TestAppendLogNativePartialRestartAndSealedRotation` verifies a half-written
signed frame creates no claim/cursor/result or native record, survives joined
restart, and delivers once after its remaining bytes and newline arrive. After
its seal checkpoint, the producer retires generation 1; missing generation 2
leaves the child running, and an encrypted generation-2 send delivers once with
exact independently observed metadata/body. Native CHECK returns exactly two
records. The acceptance and rotation controls passed together under race
detection (12.703 seconds), with integration vet/staticcheck. The immutable
gateway SHA-256 was
`33fc8c7bef3d248dde67a3103d0651b3b23ad3d8a64009f95157873ffc32e66a`.
Native fixture baseline was `96f16c3bbb8a3faac2e69684f591af63ceb58450`,
archive SHA-256
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.

`TestAppendLogRefusesOtherAuthenticatedOwner` verifies signed and encrypted
envelopes from another active configured principal. An independent invocation
of the actual verifier proves those exact envelopes authenticate under the
other identity and the same binding. The actual gateway child returns correlated
authentication failures; read-only SQLite checks find no request under any
principal, and native CHECK remains empty. Shared signer/no-intake fixtures also
preserve the directory owner control. Both controls passed together under race
detection (7.229 seconds), with integration vet/staticcheck. Gateway SHA-256:
`850b38e1a9d4f758155eb4086e6064fe7dbeb8cad1769eff946df07b28e449bc`.

`TestAppendLogNativeAuthenticationAndModeRefusals` passed under race detection
(6.760 seconds), with vet/staticcheck, against gateway SHA-256
`94e3ad87a7415d73ba5aa6df6925009217e005b7075bc280d02fd604092cef27`.
A mutated real Ed25519 JWS signature, expired/future signed envelopes, wrong
audience/target and streaming, empty correlation, bearer profile and wrong frame
version produce the expected correlated refusals before any principal's journal
intake. Native CHECK stays empty. A subsequent healthy encrypted request reaches
accepted with one attempt and exact independent author/room/body, and CHECK has
only that record. 
`TestAppendLogNativeExactSizeAndOverflowRecovery` passed with race detection
(7.478 seconds), vet/staticcheck, and gateway SHA-256
`d457acb3f061e2c97057c6fba8740c51cf1b103d0f5cfeedfacbe7f05ccc04f4`.
An otherwise valid encrypted JSON frame padded to 16,385 body bytes stops the
actual child before claim, cursor, result, any principal's journal intake or
native delivery; the producer's exact source bytes and newline remain intact.
The producer repairs that initial unclaimed source, then a fresh child accepts
the same envelope padded to exactly 16,384 bytes. Receipt acceptance reports one
attempt, the independent observer checks exact author/room/body, and CHECK has
only that record. This tests the input bound; exact 65,536-byte result publication
is established by filesystem controls, not this native test.

`TestAppendLogPendingDeliveryKillAndSignalRecovery` holds the backend after
durable admission, consumes the initial result, and waits for checkpoint/claim
cleanup before terminating the actual child. SIGKILL is verified from its native
wait status; SIGINT and SIGTERM exit successfully within three seconds. A fresh
journal owner recovers the pending job and nonempty upstream identity. Fresh
child encrypted replay preserves that identity and eventually produces one
exact independently observed native message; CHECK has exactly that record.
This establishes pending journal recovery after transport publication, not
killing inside publication, lost native acknowledgements or power-loss recovery.
All six native append-log controls passed together under race detection (25.574
seconds), with integration vet/staticcheck. Gateway SHA-256:
`b59d778d298a695161cfd3ceab2fcd5a4aaac5c3ce6c11a901787f115e44a947`.
Linux runtime was still open at this Darwin checkpoint; its separate
qualification is recorded below.


## Linux append-log native acceptance

The real append-log polling service and retained-claim adapter were separately
qualified on Ubuntu 24.04 ARM64, Docker 29.7.2 and Linux `7.0.12-linuxkit`.
Native tests reused the checksum-pinned daemon and digest-verified cross-compiled
executables from the [Linux FIFO fixture](fifo.md#linux-native-delivery-bounded-reads-and-recovery),
with production source `159afc40c4e7607fe871443002cd39596f3fb41e` and integration
Go/module source digest
`2f3e17077b032078fb140be5ab8678bdac236910bdada0e6da80cf13c0244d01`.
Artifact digests were rechecked before execution. Both runs used no network,
a read-only root/artifact mount and an automatically removed anonymous
local-filesystem volume for journal and append-log state.

`-test.run '^TestAppendLog' -test.v -test.count=1 -test.timeout=180s`
completed with terminal exit 0 in the actual Linux integration executable.
Six native controls verified:

- Signed/encrypted sends, independently observed exact ID/author/room/whole
  body, stable one-attempt accepted replay across stopped/restarted services,
  exactly two native records and complete bounded limit-one cursor pages
  through an empty caught-up boundary (0.90s).
- Another independently verified active signer was refused as the wrong
  configured append-log owner in both envelope profiles (0.24s).
- Pending durable intake through SIGKILL/SIGINT/SIGTERM after transport result
  publication and checkpoint/claim cleanup; bounded joins, retained upstream
  identity and encrypted fresh-service recovery produced one independently
  observed native record (2.24s).
- Cryptographic/freshness/authorization/mode refusals followed by healthy
  native recovery (1.05s).
- An incomplete record did not advance claim/cursor/result state or native
  history; completing it after restart delivered once, and sealed generation
  rotation accepted the next encrypted message (0.90s).
- Oversized unclaimed input was refused without native intake, then repaired
  exact-limit input reached one-attempt native acceptance (0.37s).

The actual Linux adapter executable separately passed 16 `^TestAppendLog`
controls with terminal exit 0. These cover exact record/generation/result/claim
bounds, unsafe files/names/state and quotas, authenticated owner/replay, refusals
before intake, retained cancellation claims, seal handling, retained claim/result
checkpoint rotation, lost-source recovery, publication gaps, interrupted claim
links, partial generations and replacement/truncation refusal. Its SHA256 was
`2973c2c8ca376f52e676962ac357e8c7c8a41b64583a5bdc514c67292d1d3c56`.

Raw evidence is `/tmp/airc-linux-native-159afc4/append-log-native.log` and
`/tmp/airc-linux-native-159afc4/append-log-adapter.log`; native artifact provenance
is `/tmp/airc-linux-native-159afc4/provenance.json`. This qualifies the Linux
append-log mode on this fixture without Linux race instrumentation. It does
not prove killing inside publication, lost native acknowledgements, power-loss
durability or remote-filesystem/transfer-service trust. Final full acceptance
remains in the roadmap.
