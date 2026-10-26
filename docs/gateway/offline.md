# Files and mailboxes

These carriers move [contract](contract-v1.md) requests as files in local
mailbox directories. Producers that should not keep a token in a file can
rely on the listener's [`bound_author`](authentication.md#bound-authors).
Every carrier writes into the same journal, so a re-read file or a retry on
another carrier posts once. The mailboxes allow only `send`, `receipt.get`
and `history.read`, listed explicitly.

## Local mailboxes

All three use an existing owner-only (0700) directory with trusted ancestors
and no symlinks; the gateway holds an exclusive lock on it. Inputs must be
regular 0600 files owned by the gateway user with one link, written
completely, synced and renamed into place. A file holds one
[frame](streams.md#frames) without a length prefix (no `stream`); results
are response frames of at most 64 KiB, never overwritten. A result means
durable intake, not AIRC acceptance: ask `receipt.get` later.

| `protocol` | Layout | How it runs |
|---|---|---|
| `file_mailbox` | `request.json` → claimed as `processing.json` → `response.json` | `airc-gateway mailbox-once --file CONFIG` handles one request and exits; it must be the only enabled listener |
| `directory_mailbox` | `inbox/`, `processing/`, `results/` (each 0700); inputs named `<id>.json` with `<id>` 1–64 letters, digits, `-`, `_` and equal to the frame's `correlation_id`; result under the same name | polled by `serve` every 100 ms; at most 128 inbox and 128 result files |
| `append_log` | `logs/<20-digit generation>.jsonl`, one frame per line (≤ 16384 bytes); an empty line seals a generation (≤ 8 MiB); the gateway keeps `claim.json`, `cursor.json` and publishes `response.json` | polled by `serve`; the producer only appends, seals, then starts the next generation |

A claimed input survives interruption and is replayed through the journal
on the next run. An unread result blocks the next claim (for the directory
mailbox, the next claim of that name), which bounds what the gateway writes.
Consume the result, then publish the next request.
