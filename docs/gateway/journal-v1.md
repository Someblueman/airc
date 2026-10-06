# Gateway journal v1: ownership and intake

The on-disk store API implements schema creation, transactional mutation intake,
local status lookup, and checkpoint backpressure. It is not wired into a daemon
or adapter. Durable revocation and grants are in [dispatch-grants.md](dispatch-grants.md).
Acceptance persistence is in [acceptance.md](acceptance.md). Error transitions are in [completion.md](completion.md), worker wiring in
[delivery.md](delivery.md), and expiry/pruning in [maintenance.md](maintenance.md).
Backup and service/ingress integration remain pending. Step 4 and the complete catalogue are not complete.

## Storage boundary

`store.Open(ctx, config)` requires an existing absolute, private state directory
owned by the effective user. It does not create parents. Native directory/file
descriptor checks reject immediate symlinks, wrong owners, unsafe permissions,
nonregular files and hard-linked state files. A persistent `owner.lock` file has
an exclusive nonblocking OS lock for the store lifetime; closing releases the
lock without unlinking it. Database cleanup precedes owner-lock release. A
second cooperating process cannot open the journal. Local administrators must
not bypass the lock with a separate SQLite writer or replace state files while
the owner runs.

Native support currently covers macOS and Linux filesystem checks. macOS
requires a filesystem marked local. Linux allows ext-family, XFS and Btrfs;
network, tmpfs, overlay and unknown filesystem types fail closed. Other systems
fail closed pending native ownership/locking/local-storage verification. Native
acceptance has been run only on macOS; cross-compilation is not Linux/Windows
runtime evidence. Ancestor replacement by the same trusted owner is outside
this boundary, as with provisioned credential paths.

The dependency is pinned to [modernc.org/sqlite v1.60.1](https://pkg.go.dev/modernc.org/sqlite@v1.60.1),
whose Go 1.26 minimum fits this module's unchanged Go 1.27 directive. Its required
`golang.org/x/sys v0.48.0` replaces v0.41.0; other existing direct dependencies
are unchanged. SQLite connections explicitly use WAL, `synchronous=FULL`,
foreign keys, a one-second busy timeout and automatic checkpointing after a
page. Open verifies the actual durability pragmas. See SQLite's
[WAL documentation](https://www.sqlite.org/wal.html) and
[synchronous settings](https://www.sqlite.org/pragma.html#pragma_synchronous).
One connection is pinned for the store lifetime, with serialized operations;
connection failure cannot silently reopen with lost page limits or pragmas.

`max_page_count` reserves two thirds of configured journal bytes for WAL/index
overhead, with an additional 64 KiB reserve. Admission requires a successful
truncating checkpoint before starting its transaction; busy/failed checkpoint
stops intake and leaves existing results readable. Database-page exhaustion or
transaction failure never returns a queued result. Broader fault-injected
physical disk exhaustion, crash-at-every-dispatch-boundary and total retained
storage acceptance remain required with the dispatcher/pruning implementation.

## Durable identity and admission

Times use integer Unix seconds throughout. Schema version 2 migrates version 1
journals without resetting jobs. New empty databases are
created in one transaction. Unversioned nonempty or newer schemas are refused.
The metadata pins realm, configured author/endpoint bindings, and stable
principal/binding associations. Reopening with changed bindings is refused;
credential paths/material and permissions are excluded, so credential rotation
and permission tightening do not change job identity. Adding/removing configured
bindings or principals is currently also refused; a deliberate safe registry
update mechanism remains needed rather than silently rebinding stored jobs.
No gateway/AIRC credential material is stored in the database.

`Admit` accepts a verified immutable `auth.Identity`, a send/reply request, a
trusted codec replay deadline (zero for session authentication), and current
time. It checks both identity permissions and the store's configured current
principal. The same scoped principal/binding/request ID and normalized explicit
intent coalesce into the original job. Changed body, parent, explicit policy or
validity is an ID conflict. A later valid signature cannot recreate a retained
replay marker after its result was removed.

Admission freezes the first effective expiry, retry policy, send/lookup budgets,
backoff and terminal retention. Duplicate requests with omitted defaults reuse
these values even after configuration defaults change. Global and per-principal
pending quotas are enforced inside the same transaction; duplicates still work
at full quota. Stable upstream IDs exclude credential IDs. New jobs and signed
replay markers commit together before `Result.Durable` can be returned. A
returned job is queued responsibility, not proof of an AIRC post.

`Lookup` requires receipt permission and scopes its SQL key to the verified
principal/binding. It contacts no backend, changes no budgets, and extends no
expiry. Unknown IDs return `request_unknown`. Reply parent authorization and
participant filtering at the backend remain pending. No adapter may treat these
primitive checks as complete runtime authorization.

## Evidence

Tests exercise actual private SQLite files, concurrent coalescing, intent and
policy conflicts, scope isolation, quotas, stored defaults across reopen,
permission checks, a second-owner rejection, restrictive DB/WAL/SHM modes,
symlink preservation, author rebinding, and future-schema rejection. Real SQL
trigger failure and deferred foreign-key commit failure leave no partial job;
SQLite's actual `SQLITE_FULL` page boundary produces no queued claim. A held
reader pins WAL and proves failed checkpoints backpressure subsequent intake.

Subprocesses exit without closing SQLite or releasing the owner lock: committed
job/replay state survives, while an uncommitted deletion of both rolls back.
Reopening confirms OS lock release and WAL recovery. This proves intake process
boundaries, not the still-unimplemented dispatch/retry/receipt boundaries.

```sh
go test -race -count=1 ./internal/gateway/store
```

The test fixture owns every path/process and never contacts live AIRC.

Full repository tests and race tests, vet, command builds, `go mod tidy -diff`,
and diff checks passed. `govulncheck v1.8.0` reported no vulnerabilities after
adding the pinned driver and its required dependencies. Linux/Windows store
cross-builds passed; their filesystem behavior remains unverified natively.

Schema version 3 and failed-attempt completion are described in
[completion.md](completion.md); versions 1 and 2 migrate transactionally.

Schema version 4 adds mutually exclusive native card outcomes without changing
existing message receipts. Versions 1–3 migrate transactionally. See
[profile-presence.md](profile-presence.md).

Schema version 5 adds native topic acknowledgements, mutually exclusive with
message receipts and card outcomes. Versions 1–4 migrate transactionally;
see [topic-set.md](topic-set.md).

Schema version 6 adds room-setting acknowledgements exclusively alongside
message, card and topic evidence. Versions 1–5 migrate transactionally,
including canonical saved topic snapshots. See [room-update.md](room-update.md).

Schema version 7 adds pin-mutation acknowledgements exclusively alongside
receipt, card, topic and room outcomes. Versions 1–6 migrate transactionally,
preserving whole canonical room evidence. See [pin-mutations.md](pin-mutations.md).

Schema 8 adds canonical `activity_outcome` evidence and six-way outcome
exclusivity, migrating schemas 1–7 transactionally. Whole historical ACKs survive
restart and replay after their native expiry; activity itself is not stored by
the gateway. See [activity.md](activity.md).

Schema 9 adds canonical `reply_signal_outcome` evidence and seven-way outcome
exclusivity. Versions 1–8 migrate transactionally without changing earlier
canonical evidence. Reply-signal acceptance retains the whole native acknowledgement,
uses no receipt lookup, fences completion after observed acceptance, and keeps
immutable evidence available for retrying a failed save. Accepted replay never
resends; the saved acknowledgement may describe a signal that has since expired
or been cancelled. See [reply-signals.md](reply-signals.md).

Schema 10 adds canonical `poll_outcome` evidence and eight-way exclusivity,
migrating versions 1–9 transactionally. Creation retains the whole native
receipt and result snapshot inside one blob; vote and close retain only the
whole snapshot. The ordinary receipt column remains empty for all three.
Historical replay does not issue another native mutation, and uncertain
creation retries may produce a second native ID. See [polls.md](polls.md).

Schema 12 adds bounded POSIX ingress ownership bookkeeping without changing
job identity, delivery outcomes, authorization pins or OSCORE state. It records
only the listener/name/depth and boot/IPC-namespace/queue inode identities needed
for trusted recovery, with unique queue names and compare-and-replace writes.
Versions 1–11 migrate transactionally; schema-11 controls retain OSCORE state and
delivery jobs. This table is not producer authentication or message-intake
responsibility. See [POSIX journal ownership](posix-mqueue.md#journal-ownership).
