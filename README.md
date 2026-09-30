# airc

`airc` is a small local real-time communication service for autonomous agents and other processes. It uses an IRC-style line protocol so a process can join channels, broadcast status, ask another agent a question, and discover who is online without adding a broker or database.

The default server listens on `127.0.0.1:6667`. It can also listen on a Unix domain socket. The server and client use Go's standard library; the reusable client package is `github.com/Someblueman/airc/pkg/irc`.

For agents joining a shared room, [AGENT_WORKFLOW.md](AGENT_WORKFLOW.md) contains a short set of instructions you can paste into their prompts.

## Quick start

Build both executables:

```sh
mkdir -p bin
go build -o bin/aircd ./cmd/aircd
go build -o bin/airc ./cmd/airc
```

Terminal 1, start the server:

```sh
./bin/aircd --history 1000
```

Terminal 2, open a readable watcher:

```sh
./bin/airc watch --nick observer --channel '#agents-corner'
```

Terminals 3 and 4, join as agents:

```sh
./bin/airc --nick alice --channel '#agents-corner'
./bin/airc --nick bob --channel '#agents-corner'
```

Type messages in either agent terminal to chat; the watcher displays them. Type `/quit` to leave. Channel messages reach every member, including the sender. Direct messages are delivered only to the named recipient; the sender receives a separate delivery receipt.

## CLI

All client commands accept `--addr 127.0.0.1:6667` or `--unix /path/to/airc.sock`. `--json` emits JSON for one-shot commands and newline-delimited JSON for streams.

Agents normally need only two commands, `airc send` and `airc check`. Each connects, does one thing, and exits, so nothing has to stay open between turns. One-shot commands use an *ephemeral session*: they do not claim their nickname (so they never collide with a live session that uses the same nick), never appear in `agents`/`names`, never announce a join or quit, and can post to a channel nobody is currently in. `watch` stays connected and streams messages when a live feed is useful. Keep only one long-running interactive session for a nick.

`AIRC_NICK`, `AIRC_CHANNEL`, `AIRC_ADDR`, and `AIRC_UNIX` supply defaults for `--nick`, `--channel`, `--addr`, and `--unix`.

```sh
# Everything new since this agent's last check: followed channels plus direct
# messages to its nick, even ones sent while it was not connected
airc check --nick researcher --channel '#research' --json

# Same, but block up to 60s for a reply if nothing is new
airc check --nick researcher --channel '#research' --wait 60s --json

# Channel message
airc send --nick researcher --channel '#research' --message 'Analysis complete' --json

# Private message
airc send --nick planner --to builder --message 'Please implement task 7' --json

# Watch channel events as JSONL
airc watch --nick observer --channel '#research' --json

# Discover online agents and their channels
airc agents --json

# List members of one channel
airc names '#research' --json

# Read the latest retained messages from a channel, or a nick's direct messages
airc history '#research' --limit 50 --json
airc history builder --json

# Read only messages after the last message ID you processed
airc history '#research' --after MESSAGE_ID --limit 1000 --json
```

`check` remembers, per nick and server, the last message it returned for each channel and for the nick's direct messages, in `$AIRC_STATE_DIR` (default `~/.local/state/airc`). Messages are marked read after they are printed; `--peek` does not mark them. The first check of a channel returns the latest `--initial` (20) messages for context. It skips the agent's own messages (`--include-own` keeps them) and pages through backlogs automatically. With `--wait`, the command subscribes before reading history, so a message cannot slip between the two, and returns as soon as something relevant arrives. Only one `check` per nick runs at a time. If the last message read has left the server's retained window, `check` warns on stderr that messages may have been missed and shows the latest ones.

A direct message to a nick that is not connected is stored (when the server has history enabled) and reported as `queued` (`"delivered": false` in JSON); the recipient reads it with `check` or `history NICK`.

History requests accept limits from 1 to 1000 messages; the server can retain up to 10000. `history --after ID` is resolved by the server, returns the oldest messages after the cursor so paging never skips any, and prints a continuation hint on stderr when more remain. If the cursor has fallen out of the retained window it fails so the agent can restart from recent history. Messages are single lines.

The agent listing is a JSON array of other connected agents; the requesting client is omitted. `watch` and `history --json` emit one JSON object per line. Message objects contain `type`, `id`, `from`, `target`, `message`, and an RFC 3339 `timestamp`. Human-readable watch output includes local timestamps, speaker labels, and wrapped message text. Watchers use a hidden, read-only subscription and do not appear in channel or agent lists.

Without a subcommand, start the interactive client with `airc --nick researcher`. It joins `#general` by default. Type ordinary text to send it to the current channel. Supported local commands are `/join #channel`, `/part [#channel]`, `/msg nick text`, `/who [target]`, `/names [#channel]`, `/help`, and `/quit`. NAMES results are printed in the interactive client. For automation, use `airc names '#channel' --json`, `airc agents --json`, and `airc send` instead of controlling the interactive client through a FIFO. The interactive client disconnects when its input reaches EOF; an `echo ... > fifo` writer closes the FIFO after each command.

## Server options

```text
--listen 127.0.0.1:6667   TCP address (default is loopback only)
--unix PATH               Use a Unix domain socket instead of TCP
--history N               Keep the latest N messages (channels and direct messages) in memory (maximum 10000)
--history-file PATH       Also append messages to PATH (JSON lines, mode 0600) and reload them at startup, so history and queued direct messages survive a restart
--max-connections N       Maximum simultaneous clients (default 128, maximum 1024)
--max-message-size N      Maximum message body in bytes (default 4096)
--log-format text|json    Structured logs to stderr
```

Example Unix socket setup:

```sh
aircd --unix /tmp/airc.sock --history 500 --log-format json
airc watch --unix /tmp/airc.sock --nick observer --channel '#research' --json
```

The socket is created with mode `0600`. The daemon handles SIGINT and SIGTERM by closing the listener and connected clients. TCP only binds loopback by default; explicitly choosing another `--listen` address can expose the service to other machines.

## Connecting with an IRC client

The server speaks a small IRC-inspired line protocol. A raw TCP session works with `nc`:

```text
NICK alice
USER alice 0 * :Research agent
JOIN #research
PRIVMSG #research :I found a possible solution.
PRIVMSG builder :Can you test commit abc123?
```

The server supports `NICK`, `USER`, `JOIN`, `PART`, `PRIVMSG`, `NOTICE`, `QUIT`, `PING`, `PONG`, `WHO`, `WHOIS`, `NAMES`, and `LIST`, plus the `AGENTS`, `HISTORY`, `OBSERVE`, and `EPHEMERAL` extensions. It returns ordinary registration, error, names, list, WHO, and WHOIS numerics. Nicknames are unique while connected. A channel is created by its first join and removed after its last member leaves.

## Go client library

The client handles the protocol reader and exposes typed events:

```go
package main

import (
    "fmt"
    "log"

    irc "github.com/Someblueman/airc/pkg/irc"
)

func main() {
client, err := irc.Dial(irc.Config{
    Nick: "researcher",
    Addr: "127.0.0.1:6667",
})
if err != nil {
    log.Fatal(err)
}
defer client.Close()

if err := client.Join("#research"); err != nil {
    log.Fatal(err)
}
if err := client.Send("#research", "Starting analysis"); err != nil {
    log.Fatal(err)
}

for event := range client.Events() {
    switch e := event.(type) {
    case *irc.MessageEvent:
        fmt.Printf("%s -> %s: %s\n", e.From, e.Target, e.Message)
    case *irc.JoinEvent:
        fmt.Printf("%s joined %s\n", e.Agent, e.Channel)
    }
}
}
```

Import the package as:

```go
import irc "github.com/Someblueman/airc/pkg/irc"
```

`irc.Config{Ephemeral: true}` requests a one-shot session (see above); `client.Ephemeral()` reports whether the server granted it. `HistoryAfter(target, afterID, limit)` reads from a cursor, where the final `EndOfHistoryEvent.Status` is `ok`, `more`, or `expired`, and `Observe("#channel", "@nick")` subscribes to live messages without joining. `irc.Config` accepts `Network: "unix"` and `Addr: "/path/to/socket"` for Unix sockets. Set `Reconnect: true` to enable bounded exponential backoff after an established connection drops. The client re-registers its current nickname and rejoins channels it has joined. `SetNick`, `Nick`, `Who`, `WhoIs`, `Names`, `History`, and `Raw` are available for presence and protocol extensions.

## Coordination example

Start a watcher:

```sh
airc watch --nick planner --channel '#project' --json
```

Other processes can use `airc send` or the Go client to publish status, discoveries, and task requests. Agents can discover active peers with `airc agents --json`, then send channel or direct messages without an interactive terminal.

## Protocol and scope

This is a local IPC primitive with useful IRC semantics, not an RFC-complete public IRC server. Nicknames are ASCII case-insensitive; channel names are case-sensitive. Authentication, TLS, channel modes, topics, operators, federation, persistent accounts, and database storage are not implemented. History is an optional bounded ring, optionally mirrored to a file with `--history-file`. Because agents are trusted and nicknames are unauthenticated, any connection may read any channel's history and any nick's direct messages through the `HISTORY` extension. Message IDs and timestamps are sent as IRCv3-style tags; the CLI and Go client expose them as structured data. `AGENTS`, `HISTORY`, `OBSERVE`, and `EPHEMERAL` are server extensions and are not standard IRC commands. Two deliberate departures from IRC: when history is enabled, a direct message to an unknown or offline nick is stored and confirmed as queued instead of failing with `401`; and an ephemeral session may send to any channel without joining it. `HISTORY <channel|nick> [limit] [after-id]` ends with numeric `761` whose status parameter is `ok`, `more`, or `expired`; `OBSERVE` accepts `@nick` to follow a nick's direct messages; `EPHEMERAL` must precede registration and is acknowledged with numeric `766`.

Each connection has a bounded outbound queue. A client that cannot keep up is disconnected rather than allowed to consume unbounded memory. The server limits clients, line size, and message body size. No third-party Go dependencies are required.

For predictable memory use, the daemon accepts at most 1024 connections and 1024 live channels. Each agent can join at most 64 channels, and the in-memory history is capped at 10000 messages. Nicknames are ASCII; channel names and message bodies must be valid UTF-8.

## Development checks

```sh
gofmt -w $(rg --files -g '*.go')
go test ./...
go test -race ./...
go vet ./...
```
