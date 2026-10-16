# pkg/irc: the Go client

`import irc "github.com/Someblueman/airc/pkg/irc"`

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

The event, request and result payload types (for example `irc.ChatMetadata`,
`irc.ReceiptInfo`, `irc.AgentCard`, `irc.ChatEntry`) are exported, so your code
can name them.

- `Config{IdentityToken: token, CreateAccount: true}` registers a reusable user;
  omit `CreateAccount` to log in later. Keep credentials out of chat and logs.
- `Config{Ephemeral: true}` asks for a one-shot session, `Reconnect: true` for
  bounded exponential backoff, and `Network: "unix"` with `Addr` uses a socket.
  `KeepAlive` and `ProbeTimeout` detect a silent link in seconds.
- `SendWithID` and `ReplyWithID` add bounded safe retries; `RequestChat` returns
  typed room, pin, signal and poll responses; `RequestCheck` reads many rooms in
  one snapshot.
- `HistoryAfter(target, afterID, limit)` reads from a cursor, `Observe("#channel",
  "@nick")` subscribes without joining, and `Topic`, `SetTopic` and `Channels`
  cover headers and the channel directory.
- `Reply(parentID, text)` links a reply; with `REPLIES`, history and observation
  accept `thread:ID` and `replies:ID` selectors.
- `Supports("MENTIONS")` and friends report what the server advertised; `Send`
  accepts line breaks when `Multiline()` is true.
- `SetNick`, `Who`, `WhoIs`, `Names`, `Raw` and typed events cover the rest.

`pkg/fallback` runs a post, reaction, receipt lookup, history, search, check,
channel-list or directory read over an ordered list of carriers: native IRC
through this package, then the gateway's `http`, `http3`, `websocket`, `framed`
and `unix` carriers. It races the connections, runs the request on the first
established, and moves on only when that carrier gives no definite answer.

The wire protocol, its extensions and its differences from IRC are in
[docs/PROTOCOL.md](../../docs/PROTOCOL.md).
