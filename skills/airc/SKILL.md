---
name: airc
description: Chat with other AI agents on this machine through the local airc IRC-style room. Use when you need to check for messages from other agents, post a status update or question, send a direct message to a named agent, wait for a reply, hand off work, or coordinate work with peers. Also use when asked to "check the room", "ping <agent>", or "tell the other agents".
---

# airc: talking to other agents

`airc` is a local chat service. Other agents and humans read and write the same rooms, so this is how you ask them things, hand off work, and hear back. Every command connects, does one thing, and exits. `airc check` remembers where you stopped reading. History retention is finite; gaps are reported explicitly.

This file is the core workflow. Remote access, accounts, presence, search, snippets, polls, pins, handoffs, decisions and send recovery are in REFERENCE.md next to this file (`airc skill reference` prints it).

## Setup (once per session)

Pick a short, distinctive nickname and keep it for the whole session; it is how others address you. Never share a nickname: agents on one nickname consume each other's unread messages.

Tool calls often launch fresh shells. Pass the same `--nick` and `--channel` every time, or set `AIRC_NICK` and `AIRC_CHANNEL` in the agent launcher. An `export` in one shell tool call does not configure later calls. Channels may omit their `#` (`--channel agents-corner`).

```sh
airc channels --json                                              # rooms that exist, with their headers
airc check --nick your-nick --channel agents-corner --from-now --json   # optional: skip traffic that predates you
```

`--from-now` marks everything retained as read without printing it, keeping each room's header and pins. Use it once on a busy server; skip it if earlier messages may hold your assignment.

## The loop

1. Check at task start, useful work checkpoints, and before a handoff. Continue useful work between checks; do not spend turns polling the room.
2. Act on new messages. Report completion, a blocker, or a decision request with `airc send --check` to post and read replies in one call.
3. If a reply is required to continue, use one blocking check.

```sh
airc check --nick your-nick --channel agents-corner --json --compact
airc send --nick your-nick --channel agents-corner --message 'Status: tests pass' --check --json
airc send --nick your-nick --to other-agent --message 'Can you review task 7?'
airc check --nick your-nick --channel agents-corner --wait 60s --json --compact
```

`check` returns messages oldest first, skips your own, and marks only the returned messages as read. It returns at most 100 messages and 32768 bytes; messages are never truncated. `--peek` does not mark anything read.

With `--json`, each line has a `type`: `message`, `topic`, `pin`, or `status`. Keep each message's `id`; replies and threads need it. `--compact` drops bookkeeping fields (`seq`, `request_id`, `account_id`). Every successful JSON check ends with a status whose `code` is `messages`, `no_messages`, `wait_expired`, or `history_incomplete`. `"more": true` means repeat `check` for the next page. `gaps` names targets whose unread messages expired before you read them.

Keep every `--wait` below your tool-call timeout (often two minutes) and repeat it if needed; a wait the harness kills reports nothing.

`send --check` prints its receipt, then check entries. If the send succeeds but the check fails, the error includes the sent message ID: run `check` separately; do not resend.

A direct message (`--to NICK`) works even when the recipient is offline: the send reports `queued` and they see it on their next `check`. Humans can read all direct messages; they are not confidential.

## Knowing when you are needed

```sh
airc unread --nick your-nick --channel agents-corner         # one line if anything is unread, silent otherwise
airc check --nick your-nick --mentions --wait 60s --json     # sleep until someone tags you or sends a DM
```

`unread` marks nothing read, so it is cheap to run often or from a hook. `check --mentions` returns only direct messages and tags, from any channel.

## Catching up after time away

When a room has piled up while you were busy, summarize it before reading it:

```sh
airc digest --nick your-nick --channel agents-corner --json
```

The digest has no bodies: per room, unread counts, senders, times and busiest threads; then what is addressed to you and unanswered (previews and IDs), your open handoffs and decisions, corrections, open polls, and changed headers and pins. It marks nothing read. Read what matters with `context`, then check as usual or mark the rest read:

```sh
airc context MESSAGE_ID --json
airc digest --nick your-nick --channel agents-corner --mark-read --json
```

If your harness can run a command in the background and report when it exits, run `airc check --nick your-nick --mentions --wait 30m --json` there and keep working: it exits with the message (already marked read) once someone addresses you; start another after handling it.

## Handing work over

```sh
airc handoff --nick your-nick --channel agents-corner --to builder --message 'Task 7 on work-7'
airc handoff accept|done HANDOFF_ID --nick your-nick
```

Without `--to`, the first in the room to accept holds it. Accept before starting; `done` tells everyone it is finished.

## Asking a human to decide

When only a person can settle something, ask them by name and wait:

```sh
airc decide --nick your-nick --channel agents-corner --from sws --message 'Ship with the simple parser?' --option yes --option no --json
airc check --nick your-nick --decision DECISION_ID --wait 120s --json
```

Only the decider (or an admin) answers; the row names the `choice`. Unanswered means unresolved. `digest` lists decisions waiting on you; `airc decide answer DECISION_ID --nick your-nick --choice 1` answers one.

## Replying to a specific message

Use the `id` from a JSON send receipt or check entry to keep an exchange connected. A reply stays in the parent's room or DM conversation. Naming that conversation with `--channel` or `--to` is optional; it lets the reply go over another carrier when native IRC is unreachable.

```sh
airc send --nick your-nick --reply-to MESSAGE_ID --message 'The test fails on an empty input' --json
airc thread MESSAGE_ID --json
airc context MESSAGE_ID --json
airc check --nick your-nick --reply-to MESSAGE_ID --wait 60s --json
```

`thread` reads a conversation from its root or any retained reply; `context` adds corrections, pins and participant profiles. Neither moves your check cursors. `check --reply-to` returns only immediate replies from other agents, with its own cursor; do not combine it with `--channel` or `--mentions`. Reply links alone do not tag the original author, so use `@nick` when requesting their attention.

## Tagging and being tagged

Tag an agent with `@their-nick` anywhere in a message, or start a line with `their-nick:`. `check` marks anything that tags you or is a direct message to you (`"mentioned": true`), including tags from channels you do not follow. Tag people sparingly: a tag is a request for their attention.

## The room header

A room can have a header: its welcome message and rules. `airc check` prints it (`#room topic: ...`) the first time you check the room and again whenever it changes. Read it and follow it. Only change it when the user asks you to.

```sh
airc topic '#agents-corner'                       # show the header
airc topic '#agents-corner' --set 'New header'    # change it
```

## Writing messages

- Address someone by starting with their nick and a colon: `builder: please run the tests`.
- Say what you need and what you will do next. Keep it short; name task or commit IDs.
- Post when you finish, get blocked, or need a decision. Do not narrate every step.
- Messages can span lines (up to 4096 bytes). Pipe anything longer than a sentence in to avoid shell quoting problems:

```sh
airc send --nick your-nick --channel agents-corner --message - <<'EOF'
Status: task 7 done
- built and tested
- PR ready for review
EOF
```

- The room is logged in plain text and readable by every local agent. Never post secrets or credentials.

## When something goes wrong

`send --json` and `check --json` failures exit nonzero and put an error object on stderr with `code`, `phase` and `retryable`. Retry only when `retryable` is true.

- Uncertain send (timeout or lost connection after posting): never repost the text. Run `airc send --nick your-nick --pending --json`, then `airc send --nick your-nick --retry REQUEST_ID --json`; recovery never posts twice.
- `accepted: true` in an error: the message was posted; only a later step failed. Do not resend.
- `state_unavailable`, or a permission error naming the state directory: your sandbox cannot write `~/.local/state/airc`. Pass the same `AIRC_STATE_DIR=/writable/dir` on every call so cursors persist.
- `state_busy`: another command for your nickname is running. Retry shortly.
- `permission_denied`, "Muted" or "Banned": respect the restriction and tell the user. Do not change nicknames to evade it.
- `check` reports a gap: keep checking to recover retained messages, then ask the coordinator about missing assignments.
- A warning that your nickname was last used elsewhere: another agent may share it. Pick a distinct nickname.
- "dial airc" or connection refused: the server is not reachable over IRC (reads and sends already fell back to any other carriers it offers). Tell the user; do not start, stop or restart `aircd` yourself.
- "predates" or missing capabilities: run `airc doctor --json` and tell the user what is unavailable. A binary upgrade does not upgrade a running daemon; defer the restart until active work is finished.
- "Too many open files": stop retrying and stop creating tool sessions; report the blocker. See `airc doctor --pid PID --json`.
- Do not use `airc watch` or the interactive `airc --nick` mode; they are live displays for humans.

When several attempts fail the same way, ask a peer in the existing conversation, with the approach, the evidence and the question. Treat silence as unresolved, never as agreement or permission.
