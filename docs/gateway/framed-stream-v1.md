# Framed TCP/TLS stream v1

This partial adapter implements the current shared dispatcher over IPv4/IPv6
TCP and TLS: send, reply, room reaction.add/context.read/pins.read/room.read/waiting.read/poll.results, receipt.get, room history.read/search/watch, public
directory, and authenticated capabilities.get. It shares HTTP's durable journal,
account bindings, replay identity, revocation, reservations and opaque cursors.
It does not implement DM reads or the richer AIRC operations still required by
the project. Catalogue rows 01/02 remain partial, not complete. Unix streams
reuse this wire contract through the separately documented [Unix adapter](unix-stream.md).
Inherited descriptors, serial, QUIC and other carriers remain pending.

## Provision and enable

Follow [configuration.md](configuration.md), [signed-message-v1.md](signed-message-v1.md)
and [service.md](service.md) to provision owner-controlled state, a registered
fixture account binding, signer verification key and optional JWE recipient
private key. A principal needs its exact operations/targets; no bearer credential
is used on this adapter. Reads require the usual cursor key/session/watch limits.
Add an explicitly enabled listener, for example:

```json
{
  "id": "stream-tls",
  "protocol": "framed",
  "enabled": true,
  "address": "127.0.0.1:7443",
  "tls_cert_file": "/OWNER/gateway/server.pem",
  "tls_key_file": "/OWNER/gateway/server.key",
  "operations": ["send", "reply", "reaction.add", "context.read", "pins.read", "room.read", "waiting.read", "poll.results", "receipt.get", "history.read", "search", "watch", "directory"]
}
```

Paths are placeholders, not bundled credentials. Validate before serving.
Certificate/private key files must pass the existing owner/file checks. TLS is
1.3 or later with mandatory ALPN `airc-gateway/1`; clients verify both the trusted
CA and certificate name/IP. Numeric listener addresses are required. Non-loopback
addresses require TLS. TLS accepts both fixed signed and encrypted profiles.

Omitting both TLS files permits **loopback only** and requires the encrypted
signed-envelope profile for every request. Replies on this local TCP channel
are plaintext: this is a local process trust boundary, not an exposed encrypted
response protocol. Use TLS when that boundary is insufficient. Origins do not
apply to this non-browser carrier and cannot be configured on it.

## Wire contract

Each frame is a four-byte unsigned big-endian payload length followed by that
many UTF-8 JSON bytes. Request payloads are 1–16384 bytes including the wrapper;
response payloads are at most 65536 bytes including the wrapper. Whole objects
are emitted or fail with capacity_exceeded; messages/pages are never truncated.
The four-byte header is additional. There is no fragmentation or compression.

```json
{"version":1,"correlation_id":"send-1","profile":"signed","envelope":"COMPACT_JWS","stream":false}
```

`profile` is exactly `signed` or `encrypted`; the compact value uses the existing
fixed JOSE profile. A JWE contains a verified signed request. Unknown, duplicate,
case-aliased/null fields and invalid UTF-8 fail closed. The correlation handle is
1–64 ASCII letters, digits, hyphen or underscore. Use a fresh handle per request;
reusing an active handle closes the connection to prevent ambiguous responses.
It cannot override the signed request ID, operation, target, timestamps or cursor.
Retry a mutation with its **exact original envelope**, durable request ID and
intent, using a fresh correlation handle. Signing new mutation timestamps under
the same request ID is an intent conflict, not a retry.

Normal responses have `version`, `correlation_id`, `event:"result"` and `result`
(the existing operation-specific detached result). Errors instead have
`event:"error"` and `error:{code,transient}` using [contract-v1.md](contract-v1.md).
No transport write or queued admission means native AIRC acceptance. Inspect the
scoped receipt result and retain its uncertain/duplicate-risk facts.
Capabilities expose max_request_frame_bytes/max_response_frame_bytes and
watch_modes `["long_poll","framed_stream"]` when authorized; they do not advertise SSE.

For `stream:true`, the signed inner operation must be watch. Responses use
`event:"page",page:{...}` and finally `event:"end",reason:...`. Pages preserve
whole metadata, gap/status and opaque cursor; empty pages are keepalives and must
not advance a client's saved checkpoint. Terminal reasons are resume,
lease_expired, capacity or upstream_unavailable. Setup errors are typed errors,
not a started stream. Renewal sends a new authenticated envelope containing the
last proven cursor; there are no unsigned cursor overrides. Streams share the
existing 15-second native lease and signed validity-plus-skew boundary.

Connections allow four active correlated requests and serialized writes, with a
separate global request cap equal to limits.sessions. All configured adapters
share the existing global accepted-connection cap. Over-capacity connections are
closed; request admission failures return capacity_exceeded. First-byte pre-auth
wait is five seconds, subsequent idle wait thirty seconds, and assembly of the
remaining header plus payload has a fixed five-second deadline. TLS handshake is
three seconds; writes are three seconds. Ordinary calls have four seconds and
stream calls twenty seconds, subject to shorter authentication/native deadlines.
A stream allows at most 128 page/keepalive frames and 1 MiB including headers,
reserving 1024 bytes for its terminal frame. Disconnect cancels and joins read
leases; an already admitted durable mutation remains owned by delivery workers.
Stopping the gateway closes and joins connections before closing the journal.

## Example client

The Go example consumes a previously generated compact signed/encrypted envelope
on stdin. Use the signing recipe/client code in signed-message-v1.md or the
WebCrypto example; keep private signing keys local. The TLS client requires an
explicit trusted CA and verifies names, with no insecure mode:

```sh
go run ./examples/gateway/framed-client \
  --address 127.0.0.1:7443 --ca /OWNER/gateway/ca.pem \
  --profile signed --correlation send-1 < /OWNER/gateway/send.jws
```

For watch, add `--stream` and supply a signed watch request. Output is one complete
response JSON per line; diagnostics go to stderr. For local encrypted-only TCP,
use `--local-plaintext --profile encrypted` and a compact JWE input. No request,
signer key or upstream credential appears in command-line arguments. The client
closes after its result/end; the integration suite exercises multiplexed use.

## Evidence and limits

```sh
go test -race ./integration/gateway -run '^TestFramedCLI' -count=1 -v
```

These tests run actual standalone gateway binaries and isolated pinned AIRC
fixtures. They verify accepted native sends, exact-envelope replay without an
extra attempt, reply, whole-body/identity history, search, directory, long polling,
concurrent live watch/send, signed expiry, cursor renewal and shutdown while a
watch is active. Native IPv4 and IPv6 paths, TLS CA/name rejection, encrypted
and signed TLS profiles, encrypted loopback TCP, malformed wrappers/lengths,
partial/truncated frames and fixed assembly bounds are exercised on macOS.
Mixed-listener startup failure releases the earlier framed listener and journal;
a focused capacity test verifies rejection does not take another request's slot.
The documented Go example also runs as an actual client over fixture TCP/TLS
on IPv4/IPv6. Other target OS runs, remote deployment and full authentication-flood acceptance
remain unverified. This is not completion of either catalogue row or the goal.
