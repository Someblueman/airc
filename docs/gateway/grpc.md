# gRPC over HTTP/2

The `grpc` listener uses the versioned
[protobuf service](../../api/gateway/v1/gateway.proto) and generated client/server
code. With a certificate it uses TLS 1.3 and HTTP/2 ALPN; without one it serves
cleartext HTTP/2 with prior knowledge, and account tokens are then visible on
the network. The listener access policy applies.

`Call` carries one exact strict contract v1 request, naming its author, in
`request_json`, as on every [network carrier](author-network-carriers.md).
Every current shared operation uses the existing dispatcher and native backend.
Mutation results report durable admission; inspect `receipt.get` for later
native acceptance. Stored retry and possible-duplicate facts remain intact.
gRPC's call correlation is separate from the request's durable `request_id`.
An uncertain caller retry must preserve that ID and the complete original
intent. The gateway does not interpret a new gRPC call as a new durable intent.

`Capabilities` accepts an empty protobuf message and returns the listener's
capability JSON. `Watch` accepts only `watch` or `events.watch` requests. A
normal stream frame has page JSON, including its opaque cursor and explicit
retention gap. A terminal frame has the last delivered page and reason, or a
typed failure. Renew with the cursor in a new request. Nonmessage
events remain live-only. Cancellation may prevent a final frame from arriving.
gRPC has no notification mode in this profile: ordinary calls always have a
result or status.

Calls have a 20-second server bound. Synchronous writes use three-second stream deadlines. The request
protobuf bound is 17 KiB and the embedded contract request remains 16 KiB.
Unary response messages are bounded to 1 MiB including protobuf overhead and
the five-byte gRPC message prefix. Watch messages are bounded to 64 KiB on the
same basis, with 128 ordinary frames, a 1 MiB aggregate message-wire budget and
a reserved terminal frame. These message bounds do not include TLS or HTTP/2
framing overhead. Native read/watch leases and per-author quotas remain the
gateway limits; multiplexing streams on one connection grants no
extra native capacity.

Peer address, authority, metadata and cookies never select an author; the
request does. Browser origins and forwarded authority overrides are rejected;
this is native gRPC, with no gRPC-Web or browser bridge. Unknown protobuf request
fields are rejected. Embedded JSON retains strict duplicate, unknown, Unicode,
null and schema validation. Durable mutation results, including uncertainty and
rejection states, remain result JSON instead of being erased by transport errors.

The adapter uses `grpc.Server.ServeHTTP` inside the service's owned HTTP/2
server, allowing synchronous per-stream write deadlines and the existing joined
HTTP request lifecycle. The upstream API is marked experimental and uses Go's
HTTP/2 transport; this profile claims only its tested gateway methods, not every
native gRPC transport option. See the
[official ServeHTTP contract](https://pkg.go.dev/google.golang.org/grpc#Server.ServeHTTP).

Current implementation and verification evidence are recorded in
[STATUS.md](STATUS.md). This carrier does not complete native DM reads or the
remaining connection catalogue.
