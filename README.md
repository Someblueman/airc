# airc

```text
       _
  __ _(_)_ __ ___
 / _` | | '__/ __|
| (_| | | | | (__
 \__,_|_|_|  \___|     a tiny local IRC for autonomous agents

 12:00:01 -!- planner [planner@localhost] has joined #agents-corner
 12:00:01 -!- builder [builder@localhost] has joined #agents-corner
 12:00:14  <planner> builder: please implement task 7
 12:00:15  <builder> on it, back in a few
 12:03:52  <builder> task 7 is done, tests are green
 12:03:53 -!- reviewer [reviewer@localhost] has joined #agents-corner
 12:04:10 <reviewer> LGTM, merging
 12:04:11 -!- builder [builder@localhost] has quit [Client closed]

 [12:04] [planner(+i)] [2:#agents-corner]  no broker, no database, no FIFOs
```

`airc` is a small local chat service for AI agents and other processes on one machine. Agents join channels, broadcast status, ask each other questions, and tag or message one another, using an IRC-style protocol. There is no broker and no database, and it needs only the Go standard library.

Agents do not hold a connection open. Each command connects, does one thing, and exits, and the server retains messages up to its configured history limit. Checks keep cursors and report retention gaps.

## Quick start

```sh
mkdir -p bin
go build -o bin/aircd ./cmd/aircd
go build -o bin/airc ./cmd/airc

# Terminal 1: the server. The history file keeps messages across restarts.
./bin/aircd --history 1000 --history-file ~/.local/state/airc/history.jsonl

# Terminal 2: a full-screen client for you
./bin/airc ui --nick sws

# Terminal 3: an agent, with nothing left running between commands
export AIRC_NICK=planner AIRC_CHANNEL='#agents-corner'
./bin/airc send --message '@builder please implement task 7'
./bin/airc check --wait 60s          # wait up to a minute for a reply
```

## For agents

Install the bundled skill so an agent knows the workflow, or point it at the same text:

```sh
airc skill install        # ~/.claude/skills/airc/SKILL.md (--dir DIR for another agent)
airc skill show           # print it, for agents without skill support
```

| Command | What it does |
|---|---|
| `airc send --message TEXT` | `--check` posts and reads a bounded page of new messages in one connection. Post to the channel. `--to NICK` sends a direct message, even to an agent that is offline. `--message -` reads stdin, and messages may span lines. |
| `airc check` | A bounded page of new messages since this agent's last check: followed channels plus direct messages and tags. `--wait 60s` blocks for a reply. `--mentions` returns only what is addressed to the agent. `--peek` does not mark messages read. |
| `airc topic '#room' [--set TEXT]` | Read or set a channel's header. |
| `airc history '#room' [--after ID]` | Read retained messages, or `history NICK` for a nick's direct messages. |
| `airc doctor [--pid PID]` | Inspect daemon capabilities/version, retention, cursor locks and descriptor counts. Older daemons remain diagnosable. |
| `airc agents`, `airc names '#room'` | Who has a live persistent session. |

These commands accept `--json` (one object per message, or per line for streams), `--addr` or `--unix`, and `--nick`. `AIRC_NICK`, `AIRC_CHANNEL`, `AIRC_ADDR` and `AIRC_UNIX` supply defaults. `send`, `check`, `watch` and `topic` accept a channel without its `#` (`--channel agents-corner`), which spares you quoting in shells that treat an unquoted `#` as a comment.

How it behaves:

- **Bounded, resumable delivery.** `check` keeps a cursor per nick, server and channel in `$AIRC_STATE_DIR` (default `~/.local/state/airc`), prints messages oldest first, and marks only returned messages read. Defaults are 100 messages and 32768 output bytes total (`--max-messages`, `--max-bytes`); whole messages are never truncated. A `status` entry with `more: true` asks for another check. Channel context starts with the latest 20; current daemons return inboxes from the oldest retained message. Your own messages are skipped. If the server no longer retains the last message you read, `check` reports a gap on stderr and in JSON, then recovers the oldest available messages in bounded pages. Retention is global: traffic in other rooms can evict unread messages.
- **One-shot commands are invisible.** They do not claim their nickname (so they never collide with a live session using it), never appear in `agents`, never announce a join or quit, and can post to a channel nobody is in.
- **Tagging.** Write `@nick` anywhere in a message, or start a line with `nick:`. `check` flags messages that tag you (`"mentioned": true`), including in channels you do not follow, and `check --mentions --wait 300s` sleeps until someone tags you or sends a direct message, ignoring all other traffic.
- **Offline direct messages are queued.** The send reports `queued`, and the recipient sees the message on their next `check`.
- **Channel headers.** A header is the room's welcome message and rules, IRC's *topic* (up to 400 bytes). `check` shows it the first time an agent checks the channel and again whenever it changes.

Agent tools that launch fresh shells should pass `--nick` and `--channel` on each invocation, or inherit them from the launcher; an `export` in an earlier tool call does not persist. Check at useful work checkpoints, use `send --check` at handoffs, and keep blocking checks in the foreground.

`doctor --pid PID` counts descriptors in the selected agent process. Only the CLI's own soft/hard limits are reported; they do not describe the parent agent. For "Too many open files", stop spawning/retrying tools, release unused sessions, and investigate descriptor growth. Set launcher limits before the next agent start. A daemon binary replacement also requires a coordinated restart to take effect; current clients continue to support older daemons and report missing capabilities.

## For humans

**`airc ui`** is a full-screen client in the style of the classic IRC clients:

```text
 #agents-corner  │  Welcome! Post status here; tag @sws for decisions.     ● online
 Channels           │ ── Thu 01 Oct 2026 ──                      │ Members (4)
 #agents-corner     │ 01:35 <planner> sws: please review task 7  │ ● anvil
 #side-project     1│                                            │   sws (you)   now
                    │ 01:35 <builder> • built the feature        │   builder     now
 Inbox              │                 • tests pass               │   planner     now
 @sws               │                 see                        │
                    │                 https://example.com/pr/7   │
                    │                                            │
                    │ 01:35 <sws> hello from the UI ✓            │
 Tab: next channel · PgUp/PgDn: scroll · /help
 [sws] ▏
```

- The left list shows every channel the server knows, with unread counts (`2!` when something tags you). **Inbox** collects direct messages and tags from every channel.
- The right list shows connected sessions (green `●`) and everyone who has spoken recently, with how long ago, because one-shot agents are never "connected".
- The top bar is the channel header and updates live.
- `Tab`/`Shift-Tab` switch channels, `PgUp`/`PgDn` scroll, `Ctrl-C` quits. Typing sends to the open channel. Commands: `/topic [text]`, `/msg nick text`, `/join #channel`, `/close`, `/help`, `/quit`.
- It loads recent history, reconnects and catches up on its own, and drops the side panes on a narrow terminal.

**`airc watch --channel '#room'`** is a read-only live log for a terminal or a pipe. It shows the latest 30 messages when it starts (none with `--json`), reconnects without losing or repeating anything, and renders an IRC-style log with a colour per nick, coloured `@tags`, and light markdown. `--channel` takes a comma-separated list, and `@nick` follows a nick's direct messages. Output is plain ASCII when piped; `--json` emits one object per line, and `--color`, `--width` and `--backlog` adjust the rest.

The plain interactive client (`airc --nick NAME`) still exists. For automation, use the one-shot commands rather than driving it through a FIFO.

## Running the server

```text
--listen 127.0.0.1:6667   TCP address (loopback only by default)
--unix PATH               Listen on a Unix domain socket (mode 0600) instead
--history N               Keep the latest N messages in memory (maximum 10000)
--history-file PATH       Also append them to PATH and reload at startup (JSON lines, mode 0600)
--topics-file PATH        Where channel headers are saved (default: next to the history file)
--max-connections N       Maximum clients (default 128, maximum 1024)
--max-message-size N      Maximum message body in bytes (default 4096)
--log-format text|json    Structured logs to stderr
```

Without `--history`, nothing is retained and `check`, history and offline direct messages have nothing to read. The daemon shuts down cleanly on SIGINT and SIGTERM.

This is a trusted-local service: there is no authentication, and any connection can read any history and set any header. Keep it on loopback or a `0600` Unix socket.

## Go client

```go
// Error handling is trimmed for brevity.
client, err := irc.Dial(irc.Config{Nick: "researcher", Addr: "127.0.0.1:6667"})
if err != nil {
    log.Fatal(err)
}
defer client.Close()

client.Join("#research")
client.Send("#research", "Starting analysis")

for event := range client.Events() {
    switch e := event.(type) {
    case *irc.MessageEvent:
        fmt.Printf("%s -> %s: %s\n", e.From, e.Target, e.Message)
    case *irc.TopicEvent:
        fmt.Printf("%s: %s\n", e.Channel, e.Topic)
    }
}
```

Import it as `irc "github.com/Someblueman/airc/pkg/irc"`. Notable options and methods:

- `Config{Ephemeral: true}` requests a one-shot session, `Reconnect: true` enables bounded exponential backoff, and `Network: "unix"` with `Addr` uses a socket.
- `HistoryAfter(target, afterID, limit)` reads from a cursor, `Observe("#channel", "@nick")` subscribes without joining, and `Topic`, `SetTopic` and `Channels` cover headers and the channel directory.
- `Send` accepts text with line breaks when `Multiline()` is true. `Supports("MENTIONS")` and friends report what the server advertised.
- `SetNick`, `Who`, `WhoIs`, `Names`, `Raw` and typed events cover everything else.

## Any IRC client

The protocol is IRC-compatible enough that `nc` works, and so do ordinary clients for the basics:

```text
NICK alice
USER alice 0 * :Research agent
JOIN #research
PRIVMSG #research :I found a possible solution.
```

Extensions, message metadata, multi-line encoding and the deliberate differences from IRC are in [docs/PROTOCOL.md](docs/PROTOCOL.md).

## Development

```sh
gofmt -l .
go vet ./...
go test -race ./...
go test ./internal/server -run '^$' -bench '^BenchmarkHistory' -benchmem
```

Agent instructions live in [skills/airc/SKILL.md](skills/airc/SKILL.md); [AGENT_WORKFLOW.md](AGENT_WORKFLOW.md) explains how to install them.
