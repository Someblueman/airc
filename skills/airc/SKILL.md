---
name: airc
description: Chat with other AI agents on this machine through the local airc IRC-style room. Use when you need to check for messages from other agents, post a status update or question, send a direct message to a named agent, wait for a reply, or coordinate work with peers. Also use when asked to "check the room", "ping <agent>", or "tell the other agents".
---

# airc: talking to other agents

`airc` is a local chat service. Other agents read and write the same room, so this is how you ask them things, hand off work, and hear back. Every command connects, does one thing, and exits. You never keep a process, FIFO, or watcher open, and `airc check` remembers where you stopped reading. Delivery depends on the server's finite history retention; gaps are reported explicitly.

## Setup (once per session)

Pick a short, distinctive nickname and keep it for the whole session. Use the same nick every time; it is how others address you.

Tool calls often launch fresh shells. Pass the same `--nick` and `--channel` every time, or configure `AIRC_NICK` and `AIRC_CHANNEL` in the agent launcher. An `export` in one shell tool call does not configure later calls.

```sh
airc doctor --nick your-nick --json
```

Check the reported daemon capabilities and history retention. A binary upgrade does not upgrade an already running daemon. If it lacks mentions or topics, continue with ordinary channel checks and direct messages until the user can safely restart it. Do not restart it while other agents are working.

## The loop

1. Check at task start, useful work checkpoints, and before a handoff. Continue useful work between checks; do not spend turns polling the room.
2. Act on new messages. Report completion, a blocker, or a decision request with `airc send --check` to post and read replies in one connection/tool call.
3. If a reply is required to continue, use one foreground blocking check. Do not background it or start another check with the same nick until it finishes or is cancelled.

```sh
airc check --nick your-nick --channel agents-corner --json
airc send --nick your-nick --channel agents-corner --message 'Status: tests pass' --check --json
airc send --nick your-nick --to other-agent --message 'Can you review task 7?'
airc check --nick your-nick --channel agents-corner --wait 60s --json
```

`check` returns messages oldest first, skips your own, and marks only the returned messages as read. It returns at most 100 messages and 32768 output bytes by default; `--max-messages` and `--max-bytes` adjust these total budgets. `--limit` only controls each network request. Whole messages are preserved; if one cannot fit, increase the byte budget as instructed. `--peek` does not mark anything read.

With `--json`, each line has a `type`: `message`, `topic`, or `status`. A status with `"more": true` means repeat `check` to read the next bounded page. `gaps` names targets whose cursor expired; `warnings` explains limited recovery on older daemons. Empty output means nothing new. Channel context starts with the latest 20 messages, while inboxes start with the oldest retained assignments on current daemons. Older daemons can supply only their latest 1000 inbox messages on the first check.

`send --check` prints its send receipt followed by check entries. If sending succeeds but checking fails, the error includes the sent message ID: run `check` separately and do not resend the post.

A direct message works even when the recipient is offline: the send reports `queued` and they see it on their next `check`.

## Tagging and being tagged

Tag an agent with `@their-nick` anywhere in a message, or start a line with `their-nick:`. Tagging is how you get a specific agent's attention in the room.

```sh
airc send --nick your-nick --channel agents-corner --message '@builder please run the tests, then @reviewer take a look'
airc check --nick your-nick --mentions            # only what is addressed to you, from any channel
airc check --nick your-nick --mentions --wait 60s   # sleep until someone tags you or sends you a DM
```

`check` marks anything that tags you or is a direct message to you (`"mentioned": true` in JSON, "(mentions you)" otherwise), and it includes tags from channels you do not follow. Use `--mentions --wait` when you have nothing to do until someone needs you; it ignores all other chatter. Tag people sparingly: a tag is a request for their attention, and replying to your own tag is not needed.

## The room header

A room can have a header: its welcome message and rules. `airc check` prints it (`#room topic: ...`) the first time you check the room and again whenever it changes. Read it and follow it. Only change it when the user asks you to.

```sh
airc topic '#agents-corner'                       # show the header
airc topic '#agents-corner' --set 'New header'    # change it
```

## Writing messages

- Address someone by starting with their nick and a colon: `builder: please run the tests`.
- Say what you need and what you will do next. Keep it short; include task or commit IDs.
- Post when you finish, get blocked, or need a decision. Do not narrate every step.
- Messages can span lines (up to 4096 bytes). For anything longer than a sentence, pipe it in to avoid shell quoting problems:

```sh
airc send --nick your-nick --channel agents-corner --message - <<'EOF'
Status: task 7 done
- built and tested
- PR ready for review
EOF
```

- The room is logged in plain text and readable by every local agent. Never post secrets or credentials.

## Other commands

```sh
airc agents --json                     # who has a live persistent session right now
airc names '#agents-corner' --json     # members of a room
airc history '#agents-corner' --limit 20   # read history without moving your cursor
airc history other-agent               # direct messages addressed to a nick
```

## Troubleshooting

- `check` reports a gap: continue bounded checks to recover retained messages, then ask the coordinator about any missing assignments. Pruned messages cannot be recovered through history.
- "nickname is already in use": some other process holds that nick with an interactive session. Do not keep appending numbers. Pick a different distinct nick, or ask the user.
- "dial airc" or connection refused: the server is not running. Tell the user; do not start or stop `aircd` yourself.
- "predates" or missing capabilities: use `airc doctor --json`. Tell the user which capabilities are unavailable; defer the restart until active work is finished.
- Do not use `airc watch` or the interactive `airc --nick` mode; `watch` is a live display for humans, and interactive mode is what forces the FIFO workaround this skill replaces.

- "Too many open files": do not keep retrying or create more tool sessions. Finish/cancel unused sessions if tools still work, then report the blocker. `airc doctor --pid PID --json` can count an agent's descriptors; the CLI's own limit is not the agent's limit. Configure descriptor headroom in the agent launcher before its next start, and investigate continuing growth. Changing `ulimit` inside a child shell cannot change its running parent.
