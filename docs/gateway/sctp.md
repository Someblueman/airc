# SCTP gateway profiles

Each record carries a plain author request, as on every
[network carrier](author-network-carriers.md).

The `sctp` (ordered) and `sctp_unordered` listeners use native Linux ARM64 SCTP
sockets over IPv4 or IPv6.
Configure a literal unicast address and port, such as `127.0.0.1:9000` or
`[::1]:9000`. Port zero selects an ephemeral port reported at startup. DNS names,
wildcard addresses, scoped IPv6 addresses and IPv4-mapped IPv6 addresses are
rejected. Other operating systems and architectures report this mode as
unavailable. The Linux kernel must provide SCTP.

Each association uses stream 0 and PPID 0. The listener requests one
inbound and one outbound stream and disables fragment interleaving. A receive
accumulates one whole user message through `MSG_EOR`, checks `SCTP_RCVINFO` on
every fragment, and rejects changed within-record metadata, wrong delivery flags, other
streams, other PPIDs, notifications and truncated data or control metadata.
Each reply is one native `sendmsg` record. A partial write closes the association.
Ordered listeners require flags zero; unordered listeners require exactly
`SCTP_UNORDERED` on requests and set that flag on every reply. The modes cannot
be mixed on an association. Record boundaries come from `MSG_EOR` with fragment
interleaving disabled. The SCTP stream sequence number is checked for consistency
within a fragmented record, as specified by [RFC 6458 section 5.3.5](https://www.rfc-editor.org/rfc/rfc6458.html#section-5.3.5);
it is never an application identity, replay key, cursor or ordering rule.

## Provisioning and wire contract

The listener needs only an address. TLS options and browser origins are
invalid; the listener access policy (`operations`, `targets`, `guests`,
`password_file`, `limits`) applies. Each association is one author session
whose source is the peer's IP address; an `account.identify` request sets the
association's default author.

Send one UTF-8 JSON [frame](framed-stream-v1.md) per SCTP record, with
`version: 1`, a valid `correlation_id`, optionally `profile: "plain"`, and the
contract request in `request`. There is no length prefix. The complete record
must fit 16 KiB; the request schema also limits message text to 4096 UTF-8
bytes. One request is processed at a time on an association. A client may
submit independent requests before reading their replies, using a unique
correlation ID for each outstanding request. Neither request arrival nor reply
arrival order is promised in unordered mode. Wait for a complete reply before
submitting a request that depends on it; match responses by correlation ID
and, for sends and receipt lookups, the request ID.

Each response record is one plain JSON `FrameResponse` carrying the request
correlation and a sequence starting at one for each request, at most 64 KiB.
Read clients must provide room for the whole record and preserve its boundary.
Malformed frames and other profiles close the association without intake or a
reply. Capacity and authorization refusals are correlated error responses. A
delivery receipt from SCTP itself is not AIRC acceptance; use the durable
result and `receipt.get` to establish native acceptance.

## Reading, replay and lifetime

History reads use the shared bounded page format and opaque cursor. Preserve a
cursor only after receiving the whole response; resume with that
cursor and continue through the empty caught-up page. A result that cannot fit
one response returns a capacity error rather than a partial page.

Streaming capabilities advertise `sctp_ordered_records` or
`sctp_unordered_records`, alongside `long_poll`. Each stream response carries a
sequence starting at one, including its terminal `end` or `error`.
Unordered clients must buffer records by correlation ID and sequence,
accept only a contiguous sequence, and advance a cursor only after its whole
page and all preceding pages have arrived. An `end` can arrive before
earlier pages and does not complete the stream by itself. Bound buffering to
128 page records plus one terminal record and 1 MiB of wire data per
request. Reject duplicate or invalid sequences; on a gap or expired deadline,
resume from the last contiguous cursor using a fresh correlation ID.

Retries reuse the original request ID and intent. Reconnecting or
restarting the gateway does not create another replay namespace: admission and
accepted receipts use the existing durable journal. If the reply is lost before
intake, the original intent can be retried; if acceptance was retained, replay
returns the same native message ID. An uncertain crash boundary retains the
shared journal's uncertain outcome instead of asserting acceptance.

The service's connection limit bounds associations and its request semaphore
bounds admitted work. The first record has a five-second deadline and later
idle reads have thirty seconds. Ordinary requests have four seconds; streaming
requests have twenty. Each physical write has at most three seconds, capped by
the request deadline. The per-request output budget is 1 MiB, with 4 KiB
reserved for a terminal error or end record; shared streaming page/frame limits
also apply. Shutdown closes blocked accepts, reads and writes, releases request
slots, and joins accepted association handlers.

Multiple streams, other PPIDs, multihoming/failover and remote-network behavior
are outside these profiles' qualification. The separate
[timed-reliability profile](sctp-pr-ttl.md) defines the qualified PR-SCTP mode.

## Native qualification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

### Ordered mode

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
passed on that same unchanged source. Exact terminal results and the source
snapshot are recorded in [testing.md](testing.md#ordered-sctp-acceptance-checkpoint).

### Unordered mode

On 2026-10-09, the combined ordered/unordered Linux ARM64 qualification passed
95 tests/subtests without skips: 35 adapter, 24 configuration, one service, ten
CLI and 25 native gateway cases. It used the same pinned native baseline and
restricted cached container described above.

The unordered IPv4 and IPv6 cases sent two independent requests before reading
either reply. The client checked the native unordered flag on every reply and
matched protected correlations to the original request IDs. The independent
AIRC observer collected both posts without assuming arrival order and verified
author, room and Unicode body. Five posts per family were then read through
one-message cursor pages to the empty caught-up page; a single protected history
reply above 16 KiB matched all native metadata in order.

Both modes passed lost-response replay across restart, admission-fenced retry,
authentication and live-revocation refusals, oversized-record rejection and
joined shutdown. Opposite-mode requests closed before journal or native intake.
Swapping only the correlation labels of two valid encrypted unordered requests
also closed both associations before intake, proving the envelope binding.
An unordered quiet watch verified the retained page, protected contiguous
sequences, heartbeat cursors, lease termination and association reuse. Its
consumer bounded buffering to 128 pages plus one terminal record and 1 MiB,
without assuming terminal arrival order. The local fixture does not force
network reordering or establish remote-path behavior.

The executable/test source SHA-256 is
`ba84e727f59d5480723cbcd5d8328a06ed37156495585bead91ecdf9240d6371`.
The gateway binary SHA-256 is
`89c8cdae4eaa17ad9e7df7e0ca08636e144bf68de118fa9c35322f5bd9fad007`.
Commands, binary hashes, logs and independently checked results are in
`/tmp/airc-sctp-unordered-bound-linux-native-20261009/`.
Targeted Linux ARM64 vet and staticcheck passed. Focused host race checks cover
the shared application admission/cancellation path, route loading, configuration
and CLI. The Windows AMD64 unavailable-target build also passed. Linux race
detection and other runtime targets are not claimed.

Full host normal/race tests, build, formatting, vet and staticcheck passed on
the same final source. The [unordered acceptance checkpoint](testing.md#unordered-sctp-acceptance-checkpoint)
records terminal results, the earlier intermittent MQTT-SN failure and the
subsequent passing full run.

### Ordered correlation migration

On 2026-10-09, the migrated contract passed 99 Linux ARM64 tests/subtests without
skips: 37 adapter, 24 configuration, one service, ten CLI and 27 native gateway
cases. It used the same restricted cached container and pinned native AIRC
baseline described above. Native clients verified that both listener modes
rejected swapped envelope hashes and legacy opaque labels before journal or native intake.
Both modes also passed valid IPv4/IPv6 sends, whole cursor reads, large protected
pages, restart replay, authentication refusals, bounds and joined shutdown.
The unordered watch continued to pass with protected sequence validation.

The executable/test source SHA-256 is
`588c08a11c40f429482f9fcb8aed8d406169c77bab445f40f44aa82fd05588eb`.
The gateway binary SHA-256 is
`c22f941b34b6235f9c766c610a38de987ada20e7d264916f628da75afcb55553`.
Commands, binary hashes, terminal logs and independently checked results are in
`/tmp/airc-sctp-ordered-bound-linux-native-20261009/`.
Focused host race tests and Linux ARM64 vet/staticcheck passed. Full host
normal/race tests, build, formatting, vet and staticcheck passed on the same
unchanged source. The [correlation acceptance checkpoint](testing.md#ordered-sctp-correlation-acceptance-checkpoint)
records the terminal results and source snapshot. Linux race detection and
additional runtime targets are not claimed.
