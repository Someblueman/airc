# Offline bundle import

An offline bundle carries plain send requests on removable media or any other
file transfer, for a one-shot import. It is send-only with a local intake and
receipt report; conversation reads need a return path. Real removable-media
qualification remains in [ROADMAP.md](ROADMAP.md#add-request-and-mailbox-connections).

## Format

A version-1 JSON object contains only these fields:

```json
{"version":1,"requests":[{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#lobby"},"message":"hello","author":{"nick":"field-station"}}]}
```

There must be 1–32 requests, each a contract request object of at most 16 KiB;
the whole UTF-8 document is at most 1 MiB. Duplicate, unknown, case-aliased
or null fields, non-object entries, invalid UTF-8, trailing documents,
unsupported versions and empty lists are refused. There are no attachment,
executable or archive fields.

Each request names its author (a guest, or an account with its token), or
none when the listener's `bound_author` or `import-bundle --author-nick NICK
[--author-token-file PATH]` supplies one ([local authors](local-authors.md)).
The operator who runs the import is the access gate; the container itself
grants nothing.

`ReadOfflineBundle` supports Darwin and Linux. It requires an absolute
normalized path, opens it read-only with no-follow, nonblocking and
close-on-exec flags, verifies the opened file is the same bounded regular
file, and reads at most 1 MiB + 1. Loading never renames, deletes or executes
the source, and the buffer is cleared after parsing. Other platforms report
the loader as unsupported.

## Import command

```json
{"id":"offline","protocol":"offline_bundle","enabled":true,"operations":["send"]}
```

The sole enabled ingress has no address, TLS or origins.

```sh
airc-gateway import-bundle --file CONFIG --input /absolute/path/bundle.json
```

Every entry is checked before the journal opens: a valid contract request, a
`send`, an author, and no request ID repeated for the same author within the
bundle. Any failure refuses the whole bundle with no journal and no report.

Intake is durable per entry, not an atomic batch. The author's scope and
request ID coalesce earlier work: an already imported request reports
`imported: false, replay_rejected: true` with its original result, and
changed intent under the same ID fails rather than replacing the job. A
replay can resume delivery of a pending job but never creates a second one.

The version-1 report lists `entries` in input order, each with its `author`,
`imported` and `replay_rejected` flags and the full `result` (request ID,
durability, native receipt facts, state and attempt counts). `imported: true`
means durable intake; only an accepted result is AIRC acceptance. Delivery
gets a five-second grace, then workers join before the journal is released;
pending jobs stay durable for a later import.

The command exits nonzero on any rejected replay or error, but still prints
the admitted prefix if a later entry fails capacity or storage checks. Inspect
every entry. SIGINT and SIGTERM join workers and exit with failure; a hard
kill leaves committed intent for recovery. The source file stays intact.

## Evidence

```sh
go test -race -count=1 ./internal/gateway/adapter -run OfflineBundle
go test -race -count=1 ./integration/gateway -run 'OfflineBundle|LocalAuthorsOverOfflineBundle'
```

Adapter tests cover the entry, request and whole-file bounds, strict
malformed-input refusals, opaque shell text in an executable-mode file and
symlink, FIFO, directory and oversize refusals. Integration tests run the real
command against a pinned AIRC fixture: two Unicode messages observed with the
right author and receipts, fresh-process replay rejection with one attempt,
the admitted prefix at capacity and mixed replay with new intake, SIGKILL,
SIGINT and SIGTERM while work is pending with recovery by a later import,
and preflight refusals (read requests, duplicate IDs, oversized bodies) that
create no journal.
