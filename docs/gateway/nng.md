# NNG direct local REQ0/REP0 profile

The `nng` listener integrates bounded SP/TCP framing with encrypted signed
dispatch. Frozen-source repository acceptance passed at the
[service checkpoint](testing.md#nng-service-integration-acceptance). Actual
native-client/AIRC qualification remains on the [roadmap](ROADMAP.md).

The selected profile is numeric loopback TCP, cooked REQ0 client and REP0
gateway, with mandatory compact JWE containing a signed AIRC envelope. Neither
peer endpoint type nor the wire correlation identifier grants sender authority.
The connection owner authenticates every envelope through the existing
dispatcher, retains its original signed request ID and read replay deadline,
shares session/request admission, and joins cancellation before journal closure.
Handshake, request assembly, dispatch, reply writing and idle reads have
three-second deadlines; an earlier signed read deadline still applies.
Local replies are clear whole JSON, following the existing local framed policy.
Version-1 replies contain `version`, an authenticated `request_id`, and either
a complete `result` or an `error`. Unauthenticated and admission refusals carry
no request ID. Results exceeding 65,536 bytes become a whole capacity error.
The allowed operations are send, context, history and search. TLS, non-loopback
addresses, origins, forwarding devices and watches are outside this profile.

Both peers send their complete eight-byte handshake immediately and await the
peer handshake before application traffic. The gateway advertises REP0 (0x31)
and accepts only REQ0 (0x30); signature, version and reserved bytes must match.
No other endpoint role, transport version or security negotiation is supported.

A message has an eight-byte big-endian length counting its backtrace and body.
This direct profile requires exactly one four-byte terminal request identifier,
whose high bit is set, followed by a nonempty application body of at most 65,536
bytes. Forwarding hops are outside the profile. Invalid declarations are refused
before body allocation; partial messages expose neither body nor identifier.
The reply echoes the opaque wire identifier, independently of the authenticated
AIRC request ID. Returned frames and received bodies own their buffers.

Native REQ can resend on timeout or disconnection. The adapter uses
existing durable signed-send replay rules, without treating a wire handle as a
durable ID or promising native acceptance from transport delivery. Bounded read
pages and their cursors must be returned whole. No native client has yet been
used to qualify this profile.

Primary wire sources are the [SP TCP mapping draft](https://github.com/nanomsg/nanomsg/blob/master/rfc/sp-tcp-mapping-01.txt),
the [NNG TCP implementation](https://github.com/nanomsg/nng/blob/main/src/sp/transport/tcp/tcp.c),
the [REQ0](https://github.com/nanomsg/nng/blob/main/src/sp/protocol/reqrep0/req.c)
and [REP0](https://github.com/nanomsg/nng/blob/main/src/sp/protocol/reqrep0/rep.c)
implementations, and the [REQ manual](https://nng.nanomsg.org/man/v1.10.0/nng_req.7.html).
