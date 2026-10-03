# Improvement worklist

Findings from the 2026-10-03 review, in the order they are being worked.
Tick an item when its change and tests are in the tree.

## 1. Quick fixes

- [x] 1.1 Classify state-directory and filesystem errors as `state_unavailable`
      (not retryable, hint at `AIRC_STATE_DIR`) instead of `server_unavailable`
      (`cmd/airc/outcomes.go`).
- [x] 1.2 Accept a channel without `#` in `names` and `search --target`, as
      `send`/`check`/`watch`/`topic` already do. `history NAME` stays a nick's
      direct messages by design; help and skill now say so.
- [x] 1.3 `-h`/`--help` exits 0 for every subcommand; `airc help` lists the
      missing `check`, `send` and `skill` flags.
- [x] 1.4 Recover from handler panics per session in the server
      (log the stack, close that session only).
- [x] 1.5 `Shutdown` honours its context while waiting for `messageMu`.
- [x] 1.6 Remove dead code (`dial`, `Server.address`) and fix staticcheck
      findings; add `.gitignore`.
- [x] 1.7 MCP server: set `Instructions`, describe input fields, mark
      `search` and `waiting` read-only.
- [x] 1.8 Docs drift: SKILL.md MCP tool list and stale nick-in-use advice,
      full error-code and `phase` list in AGENT_RELIABILITY.md, README TLS
      statement and daemon flag list, `AIRC_CHANNEL` in MCP.md, recommended
      maximum `--wait`.

## 2. Agent features

- [x] 2.1 Wake-up path: `airc unread` for hooks and a background
      `check --mentions --wait`, documented in docs/WAKEUP.md. (`check` already
      exits on the first mention and keeps cursors, so no `watch` change was
      needed.)
- [x] 2.2 `check --compact` (drops `seq`, `request_id`, `account_id`; keeps
      IDs) and `check --from-now` (baseline cursors without reading backlog).
      Human `check` output shows message IDs.
- [x] 2.3 `airc channels` (room discovery) and `airc unread` (per-room and
      inbox unread/mention counts without moving cursors).
- [x] 2.4 Warn when a different agent session reuses a nick's cursor state.
- [x] 2.5 MCP tools for `presence`, `profile`, `history`, `channels`,
      `unread`.
- [x] 2.6 Shorten SKILL.md to the core workflow; move accounts, operators,
      bots, polls and recovery detail behind references.

## 3. Robustness and structure

- [x] 3.1 One atomic-write helper (temp, fsync, rename, directory fsync) used
      by every state file; corrupt topics/profiles/cursor files are set aside
      as `.bad` instead of being fatal.
- [x] 3.2 A failed history compaction keeps appending to the still-valid
      file; log rotation reopens after a failed rename.
- [x] 3.3 Outbox lock waits up to 5s for another send on the same nick, so
      concurrent sends succeed. Corrupt outbox is set
      aside with a clear message.
- [x] 3.4 Every subcommand reports `--json` failures as one structured
      error on stderr (handled once in `run`, which also hides the flag
      package's usage dump in JSON mode).
- [x] 3.5 Reduce duplication in `cmd/airc`: `outbox.remove`, `sendBody`
      split out of `runSendSession`, typed check for the "no longer retained"
      rejection; `awaitEvent`, `closeOnCancel` and a shared `backoff` in
      `cmd/airc/await.go`, used by the three reconnect loops. A few wait loops
      keep their own form because folding them in would change error text.
- [x] 3.6 Tests: `FuzzParse` and `FuzzDecodeBody`, torn-last-record history
      restore (which found and fixed a real loss when the final newline was
      missing), two tests that flaked under `-race` fixed (`pkg/bot` fairness,
      SASL nickname release). No CI workflow: run gofmt, `go vet`,
      `staticcheck` and `go test -race ./...` locally. Blocking-check, MCP
      and outage tests now wait on the daemon's observe acknowledgement
      (`cmd/airc/tap_test.go`) instead of sleeping. The four
      expiry tests advance an injectable server clock (`Config.Now`) instead
      of waiting.

## 4. Performance

- [x] 4.1 Durability mode for history and state writes (`aircd --sync
      full|fsync|none`, default `full`); plain fsync for the CLI outbox file
      (`AIRC_SYNC=full` restores the device flush).
- [x] 4.2 Room quota insertion without a full ring scan: per-room counts
      are kept incrementally and removal shifts from the nearer end, which also
      makes replay with quotas cheap.
- [x] 4.3 Raise the default connection limit (128 to 512) and document sizing.
- [x] 4.4 Read-only and signal CHAT commands do not take `messageMu`.
- [x] 4.5 UI draft saves no longer fsync (was a full device flush per
      keystroke); a failed draft save is a status warning, not an exit.

## Found in review, not yet addressed

- Group commit for history appends, if `--sync fsync` is not enough.

## 5. Review findings, since addressed

- [x] 5.1 `airc service install --sync full|fsync|none` is passed to
      `aircd --sync` when not `full`, so a managed service can use the faster
      modes.
- [x] 5.3 `pkg/irc` event structs use the exported `irc.X` aliases, gathered
      in `pkg/irc/types.go`; token validation moved to `internal/tokenfmt` so
      the client library no longer imports `internal/admin`.
- [x] 5.4 Server status reports `observers` (connections subscribed to live
      traffic), which the TLS MCP test uses to cancel a wait only after it has
      subscribed.
- [x] 5.5 A `check` that arrives while another check on the same nickname
      holds the cursor lock now waits up to 2s instead of failing with
      `state_busy` (found by stress-running the MCP tests).
- [x] 5.2 Registration abuse on a remote listener: `airc admin account-delete`
      and `account-list` free a nickname (write-before-apply; a live session
      keeps its connection but loses account-based operator rights); a
      connection is closed after 3 failed credentials (PASS, AUTH/REGISTER,
      SASL, OPER); non-loopback TCP peers are capped at a quarter of
      `MaxConnections` (minimum 4) unregistered connections each; one
      `REGISTER` creates at most one account per connection. A remote
      address may create at most 8 accounts per hour, so reconnecting does not
      get around the per-connection limit. Still open: counting pre-credential
      traffic (commands sent without `PASS`) as a failure.

## Not planned here (needs a product decision)

The docs currently disclaim task ownership and summaries, so these are left
out until that changes: advisory claims/locks and structured handoffs,
deadlines with read acknowledgements, a structured catch-up digest, and
attachments beyond the 4096-byte message limit.
