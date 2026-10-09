# Ordered SCTP gateway profile

The `sctp` listener uses native Linux ARM64 SCTP sockets over IPv4 or IPv6.
Configure a literal unicast address and port, such as `127.0.0.1:9000` or
`[::1]:9000`. Port zero selects an ephemeral port reported at startup. DNS names,
wildcard addresses, scoped IPv6 addresses and IPv4-mapped IPv6 addresses are
rejected. Other operating systems and architectures report this mode as
unavailable. The Linux kernel must provide SCTP.

Each association uses ordered stream 0 and PPID 0. The listener requests one
inbound and one outbound stream and disables fragment interleaving. A receive
accumulates one whole user message through `MSG_EOR`, checks `SCTP_RCVINFO` on
every fragment, and rejects changed message identity, unordered flags, other
streams, other PPIDs, notifications and truncated data or control metadata.
Each reply is one native `sendmsg` record. A partial write closes the association.

## Provisioning and wire contract

Use the existing principal signing keys and gateway recipient keys for encrypted
detached requests. Configure `response_signing_key` and exactly one return route
for every principal on the listener:

```json
{
  "id": "sctp-reply",
  "principal": "writer",
  "protocol": "sctp",
  "listener": "ordered",
  "destination": "connection",
  "recipient_key_id": "reader",
  "recipient_public_key_file": "/private/reader-public.pem"
}
```

This route chooses a provisioned response recipient from the authenticated
principal and binding. An SCTP address, association identifier or caller-supplied
field never chooses an AIRC identity or a reply key. TLS options, anonymous
access and browser origins are invalid for this listener.

Send one UTF-8 JSON `FrameRequest` per SCTP record, with `version: 1`, a valid
`correlation_id`, `profile: "encrypted"`, and the compact encrypted signed
envelope in `envelope`. There is no length prefix. The complete record must fit
16 KiB; the existing request schema also limits message text to 4096 UTF-8 bytes.
One request is active at a time on an association. Wait for its complete reply
before submitting the next request.

The response record is the raw compact encrypted response token. Verify its
signature, recipient, lifetime and exact realm/principal/binding/route scope
using the existing [protected response format](udp.md), then decode its
`FrameResponse` payload. The payload carries the request correlation and a
sequence starting at one for each request. Responses allow 32 KiB of plaintext
and at most 64 KiB per sealed wire record. Read clients must provide room for the
whole record and preserve its boundary.

Malformed, signed-only, expired, wrong-audience and unverified requests close the
association without intake or a reply. A missing configured response route is
also refused before intake. Authenticated capacity and live authorization
refusals use protected error responses; a live journal revocation does not allow
native submission. A delivery receipt from SCTP itself is not AIRC acceptance.
Use the durable result and `receipt.get` to establish native acceptance.

## Reading, replay and lifetime

History reads use the shared bounded page format and opaque cursor. Preserve a
cursor only after receiving and verifying the whole response; resume with that
cursor and continue through the empty caught-up page. A result that cannot fit
one protected response returns a capacity error rather than a partial page.

While the envelope remains valid, retries retain the original signed AIRC
request ID and intent. Reconnecting or
restarting the gateway does not create another replay namespace: admission and
accepted receipts use the existing durable journal. If the reply is lost before
intake, the original intent can be retried; if acceptance was retained, replay
returns the same native message ID. An uncertain crash boundary retains the
shared journal's uncertain outcome instead of asserting acceptance.

The service's connection limit bounds associations and its request semaphore
bounds admitted work. The first record has a five-second deadline and later
idle reads have thirty seconds. Ordinary requests have four seconds; streaming
requests have twenty. Each physical write has at most three seconds, capped by
the request deadline. The per-request sealed output budget is 1 MiB, with 4 KiB
reserved for a terminal error or end record; shared streaming page/frame limits
also apply. Shutdown closes blocked accepts, reads and writes, releases request
slots, and joins accepted association handlers.

Unordered messages, partial reliability, multiple streams, other PPIDs,
multihoming/failover and remote-network behavior are outside this ordered mode's
qualification. The separate roadmap entries retain unordered and
partial-reliability work.

## Native qualification

On 2026-10-09, the combined Linux ARM64 qualification passed 66 tests/subtests
without skips: 24 adapter, 23 configuration, one service, eight CLI and ten
native gateway cases. Binaries were cross-compiled with Go 1.27.1 and
`CGO_ENABLED=0` and executed in the already-cached Ubuntu ARM64 image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`.
The isolated runner used `--pull=never --network none --read-only
--cap-drop=ALL --security-opt=no-new-privileges`, a read-only artifact mount and
a disposable `/tmp` volume. No installation or host network changes were needed.

The independent socket client verified IPv4 and IPv6 ordered records, exact
Unicode/long-message author, room and body through a native AIRC observer,
one-message history pages through cursor exhaustion, and a whole protected
history reply larger than 16 KiB. Complete native metadata matched both the
cursor collection and the large reply. Lost-response replay after a gateway
SIGKILL/restart retained one native message ID and one send attempt after
acceptance had been durably recorded through a second association.

A separate crash/retry test held a SQLite write transaction before transport
submission, verified no journal job or native delivery, killed the gateway,
released the admission fence and retried the exact signed frame bytes. One
accepted native post resulted. This proves recovery with admission fenced; it
does not claim that userspace consumed the original SCTP record before the kill.

The same native run verified silent unauthenticated refusals, protected live
revocation refusal with zero intake, fatal oversized-record rejection, and joined
idle-association shutdown. Socket checks covered short caller buffers, invalid
record metadata, ordered reply sizes above 16 KiB, read/write deadlines, and
close waking blocked accept/read/write calls. The write test fills a bounded
send queue against an undrained peer, then checks actual deadline and close
wakeups. Host race tests separately cover protected capacity refusal before
intake and cancellation releasing the application request slot.

The final executable/test source SHA-256 is
`250ffc97b924c441f5b298fbe5e21e367ca9d1c1b1570b56f9d1eb33367fc207`.
The gateway binary SHA-256 is
`e6e3ac21279b4d472e12a666a741da9ced53a2b3a5fc99462b0fe7a5a157ebd8`;
the pinned native AIRC binary SHA-256 is
`1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`.
The native baseline is `96f16c3bbb8a3faac2e69684f591af63ceb58450` with source
archive SHA-256
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
Commands, binary hashes, logs and independently checked results are in
`/tmp/airc-sctp-qualified-linux-native-20261009/`. Targeted Linux vet and the
Windows AMD64 unavailable-target build also pass. Linux race detection and
Windows runtime behavior are not claimed.

Full Darwin ARM64 normal/race tests, build, formatting, vet and staticcheck also
pass on this same unchanged source. Exact terminal results and the source
snapshot are recorded in [testing.md](testing.md#ordered-sctp-acceptance-checkpoint).
