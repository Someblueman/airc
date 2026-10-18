# Native tunnels

`irc_websocket` and `irc_connect` carry AIRC's own protocol through the
gateway, byte for byte, instead of the [contract](contract-v1.md). A client
on them is a native client: every command, long-lived sessions, direct
messages, threads, `RESUME` and administration all work, through port 443
and HTTP proxies. The gateway opens the upstream as the author sessions do
(its Unix socket, or TCP with the upstream's TLS), sends `WEBIRC` with the
client's source first, so aircd applies its per-address limits to the real
client, and then relays. aircd itself admits the client: its `PASS` (the
access token), nickname and account credential travel inside the tunnel.

A native tunnel takes no `operations`, `targets`, `guests` or
`password_file`, and its `limits` bound connections only (`connections`,
`connections_per_source`). It needs the `upstream` with its
`gateway_token_file`: without `WEBIRC` aircd would see every tunnelled
client as the gateway's own local peer, exempt from its per-address
limits, so the configuration is refused. TLS is optional, as on
IRC; with a certificate the tunnel is `wss://` or HTTPS. Nothing is
journaled: durability is native's (`SAFE_RETRY`, receipts, `RETRY`).

```json
{"id": "wsirc", "protocol": "irc_websocket", "enabled": true, "address": "0.0.0.0:6689",
 "allowed_origins": ["https://chat.example"]},
{"id": "tunnel", "protocol": "irc_connect", "enabled": true, "address": "0.0.0.0:6690"}
```

## IRC over WebSocket

`protocol: "irc_websocket"` serves `GET /` and `GET /v1/irc` as an RFC 6455
WebSocket with the IRCv3 convention web clients and bouncers speak: each
text or binary message is one IRC line without its CRLF (a trailing CRLF is
tolerated, a message holding two lines closes the socket). The
subprotocols `text.ircv3.net` and `binary.ircv3.net` are offered; a client
that names neither gets text messages, and a line that is not UTF-8 is
sent as binary. `allowed_origins` admits browsers as for the other
WebSocket carriers; clients without an `Origin` are always admitted.

```js
const ws = new WebSocket("wss://gw.example:6689/", ["text.ircv3.net"]);
ws.onopen = () => { ws.send("NICK web"); ws.send("USER web 0 * :web"); };
ws.onmessage = (m) => console.log(m.data); // ":server 001 web :Welcome to airc, web"
```

## IRC through HTTP CONNECT

`protocol: "irc_connect"` answers `CONNECT` (HTTP/1.1, and HTTP/2 under TLS
with one tunnel per connection, so the listener's connection limits bound
the tunnels) with `200` and then relays the stream to the upstream; other methods get
`405`. The authority the client names is ignored: the tunnel always ends at
this gateway's upstream, so this is not a general proxy. Lines pipelined
behind the `CONNECT` are relayed too. Any IRC client with a proxy setting
can use it (WeeChat: `/proxy add gw http gw.example 6690`, then connect the
server through that proxy without TLS of its own; the tunnel's TLS, when
configured, protects the hop).

## The airc client

`airc` treats both as native carriers. Advertised in `005 CARRIERS` (the
bundled service: `airc service install --gateway irc_websocket,irc_connect`,
ports 6689 and 6690), they are raced beside aircd's own port by every
command, including the ones that need native IRC and so had no fallback
before (`unread`, direct messages, threads, `--after` paging, chat updates,
the MCP stdio adapter's session). The tunnel that last answered in native
IRC's place leads the next race. `send --json` names the tunnel in
`carrier`. Over `http+tls`-style entries (`irc_websocket+tls:6689`) a
registered user's token and the access token are sent only over TLS, a
socket or loopback, as everywhere.
