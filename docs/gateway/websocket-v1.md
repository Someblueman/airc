# WebSocket gateway v1

This partial adapter carries the shared authenticated operations over RFC 6455
WebSocket text messages: send, reply, room reaction.add/context.read/pins.read/room.read/waiting.read/poll.results/topic.read, receipt.get, room history.read/search/watch,
public directory and capabilities.get. Catalogue row 19 still needs the
mode-specific acceptance in [ROADMAP.md](ROADMAP.md). DM reads are unimplemented;
richer AIRC operations are optional. This is HTTP/1.1 Upgrade;
HTTP/2 extended CONNECT and WebSocket over HTTP/3 are not implemented.

## Configuration and authentication

Use the existing owner-provisioned account binding, signer verification keys,
optional JWE recipient private key, journal and cursor provisioning instructions
in [configuration.md](configuration.md) and [signed-message-v1.md](signed-message-v1.md).
Add an explicitly enabled `protocol:"websocket"` listener with numeric address,
operation allowlist and optional TLS certificate/key files. The principal's
operations/targets and the durable permission generation still constrain every
request. Example listener (paths are owner-supplied placeholders):

```json
{
  "id":"websocket-tls",
  "protocol":"websocket",
  "enabled":true,
  "address":"127.0.0.1:7444",
  "tls_cert_file":"/OWNER/gateway/server.pem",
  "tls_key_file":"/OWNER/gateway/server.key",
  "allowed_origins":["https://client.example"],
  "operations":["send","reply","reaction.add","context.read","pins.read","room.read","waiting.read","poll.results","topic.read","receipt.get","history.read","search","watch","directory"]
}
```

Remote listeners require TLS 1.3 or later. Clients verify CA/name; browsers use
their normal trusted certificate store. Loopback plaintext accepts encrypted
signed envelopes only and returns unencrypted responses, so this requires local
process trust. WSS accepts signed or encrypted envelopes. No bearer, URL/query credential, Idempotency-Key or unsigned cursor is accepted.
Ambient browser cookies are ignored and carry no identity authority; each
application frame still requires its signed/encrypted envelope. A successful
upgrade does not authenticate an AIRC principal; each frame authenticates its
own fixed-profile envelope. No account is provisioned by an upgrade.

`GET /v1/socket` requires WebSocket subprotocol `airc-gateway.v1`. Browser Origin
must exactly equal an owner-configured serialized HTTP(S) origin, including its
port. Unknown, duplicate and opaque origins fail, even for the gateway's own host.
Native clients may omit Origin; they still need a valid signed/encrypted frame.
There is no wildcard or origin-based identity. Compression is disabled.

## Messages, limits and lifecycle

Use the same versioned request/response JSON wrapper as
[framed-stream-v1.md](framed-stream-v1.md), without its four-byte length header.
One WebSocket **text message** is one whole JSON object; binary or malformed
messages close the connection. Each request includes version, fresh connection
correlation_id, fixed profile, compact envelope and optional stream:true. The
correlation handle cannot replace the signed durable RequestID or cursor. Retry
a mutation with its exact original envelope and a new handle. A duplicate active
handle closes the connection before any failure can alias another request.

Response events are result/error or page/end for a streaming watch. Whole pages
and omission/gap metadata remain intact. Watch renewal must carry a newly signed
envelope containing the last proven opaque cursor. Capabilities advertise
watch_modes `["long_poll","websocket"]`, not SSE; the listener uses the current
shared dispatch permissions and frame limits.

Requests are at most 16 KiB including wrapper; responses at most 64 KiB.
There are four active requests per connection and a global correlated-request
pool shared with framed TCP/TLS listeners. All ingress listeners share the
existing accepted-connection cap. Frames/pages that cannot fit fail with a typed
capacity error rather than truncating bodies. Streaming uses the existing
128-page/1-MiB budget and reserved terminal allowance, native 15-second lease,
signed validity-plus-skew, bounded setup and caller deadlines.

The first application message has a five-second pre-auth deadline; subsequent
reads have a thirty-second idle deadline. Writes have three seconds. Pings every ten seconds require a pong within three seconds;
liveness is bounded and owned by the upgraded handler. A write/heartbeat failure
or disconnect cancels and joins its active reads and requests. The HTTP handler
stays alive for the upgraded connection; service-owned context cancellation
closes it, and the service joins these handlers before closing the journal.
`http.Server.Shutdown` alone is not treated as ownership of hijacked sockets.
A durable admitted mutation remains owned by delivery workers after disconnect.

## Browser example

[envelopes-websocket.js](../../examples/gateway/envelopes-websocket.js) exports
`openEnvelopeSocket(url,{signal})` with `call(profile,compactEnvelope)`,
`watch(profile,compactEnvelope)` and `close()`. Reuse `signEnvelope` and
`encryptEnvelope` from the existing WebCrypto example; signer private keys remain
local and are not passed as URL parameters. A call returns the existing detached
operation result. A watch yields `{event,value}` pages and a terminal reason;
save a checkpoint only after consuming its page. Client queues, pending calls,
write buffering, frame sizes and request deadlines are bounded.

Cancelling a call/watch or returning from its iterator closes the whole socket,
which releases all of its read leases. Open a new socket and use a newly
authenticated cursor envelope to resume. The client does not silently regenerate
mutation timestamps or retry a possibly accepted mutation. Typed failures are
surfaced to the caller, including uncertain receipt facts in results.

```js
const socket = await openEnvelopeSocket("wss://gateway.example/v1/socket");
const result = await socket.call("signed", compactSignedEnvelope);
await socket.close();
```

## Acceptance

The adapter uses pinned `github.com/coder/websocket v1.8.15`; its context-aware
connection and read-limit APIs are documented by the
[upstream package](https://pkg.go.dev/github.com/coder/websocket@v1.8.15).

```sh
go test -race ./integration/gateway -run '^(TestWebSocketCLI|TestFramedCLI)' -count=1 -v
AGW_BROWSER_PROBE=1 go test -race ./integration/gateway -run '^TestBrowserWebSocketCryptoAndLiveWatch$' -count=1 -v
```

The native suite uses actual standalone gateway binaries and isolated pinned
AIRC fixtures. The browser command requires opening its printed owner-fixture
URL in an actual browser; a compiled HTML file is not browser acceptance.
Native tests cover shared operations, concurrent watch/send, exact replay and
retry across a disconnected socket, signed expiry/cursor renewal, joined shutdown,
IPv4/IPv6, TLS trust/name rejection, origin and mixed-credential rejection, ambient-cookie non-authority,
malformed/binary/oversized messages, duplicate active handles and pre-auth/
missing-pong deadlines. Framed transport
regressions run through the same operation conformance checks. The browser probe
uses nonextractable fixture keys and an independent native observer to confirm
whole send/reply/live bodies, reactions and parent/thread facts. The real Chrome probe
also checks ambient browser cookies, pre-aborted cancellation and absence of
unhandled promise rejections. Reaction-specific evidence is in [reactions.md](reactions.md).
Native tests and browser execution are verified on macOS; browser WSS with a
trusted remote certificate remains unverified.

Other browser engines, target OS runs, remote deployment and full
slow-consumer/authentication-flood acceptance remain unverified. The full
catalogue and remaining AIRC functionality are not complete.
