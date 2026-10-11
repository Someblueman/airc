# gRPC over HTTP/2 TLS

Plain author requests need no principal, bearer or key on this carrier; see
[author network carriers](author-network-carriers.md). The rest of this page
describes the legacy signed/encrypted profile where it differs.

The `grpc` listener uses the versioned
[protobuf service](../../api/gateway/v1/gateway.proto) and generated client/server
code. TLS is mandatory even on loopback, with TLS 1.3 and HTTP/2 ALPN. A listener
must explicitly allow each operation, and the bearer principal's permissions
remain the other half of the authorization intersection.

`Call` carries one exact strict gateway v1 JSON envelope inside protobuf bytes.
Every current shared operation uses the existing dispatcher and native backend.
Mutation results report durable admission; inspect `receipt.get` for later
native acceptance. Stored retry and possible-duplicate facts remain intact.
gRPC's call correlation is separate from the envelope's durable `request_id`.
An uncertain caller retry must preserve that ID and the complete original
intent. The gateway does not interpret a new gRPC call as a new durable intent.

`Capabilities` accepts an empty protobuf message and returns permission-scoped
capability JSON. `Watch` accepts only `watch` or `events.watch` envelopes. A
normal stream frame has page JSON, including its opaque cursor and explicit
retention gap. A terminal frame has the last delivered page and reason, or a
typed failure. Renew with the cursor in a new authenticated request. Nonmessage
events remain live-only. Cancellation may prevent a final frame from arriving.
gRPC has no notification mode in this profile: ordinary calls always have a
result or status.

Calls have a 20-second server bound; authentication uses a shorter five-second
bound. Synchronous writes use three-second stream deadlines. The request
protobuf bound is 17 KiB and the embedded gateway envelope remains 16 KiB.
Unary response messages are bounded to 1 MiB including protobuf overhead and
the five-byte gRPC message prefix. Watch messages are bounded to 64 KiB on the
same basis, with 128 ordinary frames, a 1 MiB aggregate message-wire budget and
a reserved terminal frame. These message bounds do not include TLS or HTTP/2
framing overhead. Native read/watch leases and per-principal quotas remain the
existing gateway limits; multiplexing streams on one connection grants no
extra native capacity.

Bearer metadata must contain exactly one `authorization: Bearer <token>` value.
Peer address, authority, cookies and client-supplied identity fields grant no
authority. Browser origins and forwarded authority overrides are rejected;
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
