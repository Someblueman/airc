# SCTP timed-reliability profile

The `sctp_pr_ttl` profile selects native ordered SCTP records on Linux ARM64,
stream 0 and PPID 0, with `SCTP_PR_SCTP_TTL` fixed at 1000 milliseconds. It uses
one-to-one sockets over literal unicast IPv4 or IPv6 addresses. Other targets
remain unavailable. There is no configurable expiry policy or reliable fallback.

Use the plain frame and response contract in [SCTP](sctp.md): each record
carries a plain author request, as on every
[network carrier](author-network-carriers.md). The listener protocol is
`sctp_pr_ttl`.

## Native delivery contract

The listener enables PR-SCTP before association setup and requires negotiated
PR support from each accepted peer. A peer without support is closed while the
listener continues accepting other connections. Clients must likewise enable
PR support before connecting and verify support on the established association.

Each gateway reply uses `SCTP_SNDINFO` for ordered stream 0/PPID 0 and a separate
`SCTP_PRINFO` with the fixed TTL. Clients use the same policy when submitting
requests. This TTL is a sender policy: `SCTP_RCVINFO` does not disclose the peer's
per-record policy or expiry value, so negotiation does not prove that an inbound
record used that TTL. The gateway enforces its outgoing policy and authenticates
every complete incoming record independently of the sender's policy.

The TTL is a sender-side message lifetime, separate from the socket write
deadline. It allows SCTP to abandon data; it is not an end-to-end delivery or
application-processing deadline. A successful local write does not establish
delivery, journal admission or native AIRC acceptance. Use a protected durable
receipt and `receipt.get` to establish the native outcome. The timed policy is
defined by [RFC 6458 section 5.3.7](https://www.rfc-editor.org/rfc/rfc6458.html#section-5.3.7)
and [RFC 3758 section 4](https://www.rfc-editor.org/rfc/rfc3758.html#section-4).

The existing request/reply record limits, whole-record assembly, stream/PPID
checks, protected output budgets and joined shutdown apply. The receiver
subscribes to partial-delivery notifications. An aborted partial delivery closes
the association; no buffered prefix or later tail is parsed as an application
request. Clients must also discard an aborted partial response in full. See
[RFC 6458 section 6.1.7](https://www.rfc-editor.org/rfc/rfc6458.html#section-6.1.7)
for the native abort notification.

## Reading and retry

An expired request may never reach the journal. An expired reply may hide a
completed native send. Preserve the request ID and follow the
[SCTP replay contract](sctp.md#reading-replay-and-lifetime) when retrying, including
after reconnecting. Retries do not create a new logical send. Never treat expiry
or an absent reply as proof that a send did not happen.

For a bounded history read, advance its cursor only after receiving the whole
response. If a page expires, retry from the last committed cursor.
Streaming advertises `sctp_pr_ttl_records` and uses response sequences
starting at one. Ordered delivery can still have gaps because records expire.
Apply only contiguous sequences and preserve the last complete page's
cursor. A later terminal record does not complete a stream with a gap. Resume
from the last contiguous cursor with a fresh correlation ID when a record
is missing or the bounded deadline expires. Keep the existing limit of 128 pages
plus one terminal record and 1 MiB of output per request.

## Native qualification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

On 2026-10-09, the selected profile passed native Linux ARM64 qualification
with a real SCTP client and an independent native AIRC observer. IPv4 and IPv6
send tests checked author, target and body, exhausted bounded cursor history
through its empty terminal page, and verified a complete protected reply larger
than 16 KiB. The suite also passed quiet watches with contiguous protected
sequences, replay after restart, admission-fenced retry, authentication and
correlation refusals, wrong ordering, oversized records and joined shutdown.

The expiry test paused only its owned gateway process, used bounded reliable
capabilities requests to leave an 8 KiB receive window, and submitted a valid
16 KiB encrypted request with the selected TTL. The client's kernel reported
`abandoned_sent` increasing from zero to one. The journal and native history
stayed empty; resubmitting the exact saved envelope on a fresh association
produced one correctly attributed native message and one send attempt.
Available filler replies were authenticated, but the test did not require
all replies after a partial-delivery abort closed the association.

Separate native adapter checks proved expiry through the production reply
writer, complete later-record delivery, rejection of a peer with PR explicitly
disabled, and refusal of an aborted encrypted request before journal admission.
The listener also survives an interrupted accept: signal-driven expiry tests
exposed an `EINTR` exit, which is now retried. A deterministic interruption test
and 20 repeated native expiry/retry runs passed after the fix.

The final combined run passed 120 cases without failures or skips: 42 adapter,
25 configuration, one service, 11 CLI and 41 gateway cases. It used Go 1.27.1
cross-compilation with `GOOS=linux GOARCH=arm64 CGO_ENABLED=0` and the existing
image `sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`,
with no network, a read-only root, all capabilities dropped, no new privileges,
read-only binaries and a private temporary volume. The pinned native AIRC
baseline remains `96f16c3bbb8a3faac2e69684f591af63ceb58450`.

Commands, binaries, logs and verified results are retained in
`/tmp/airc-sctp-pr-ttl-linux-native-final-20261009/`.
The executable/test source SHA-256 is
`b492a44d13f52d8377dbd7aff72f405bdb89ddf37c9ac240829c9ae0af31d373`;
the gateway binary SHA-256 is
`5ef73936b0b329a889b4e898492fed3906b55677828bc6bab0e3fa5aa4ed4418`.
Full host normal/race, build and static acceptance passed on the same source;
see the [acceptance checkpoint](testing.md#sctp-timed-reliability-acceptance-checkpoint).
Linux race detection, remote paths,
multihoming, additional streams/PPIDs, other reliability policies and other
runtime targets are not claimed.
