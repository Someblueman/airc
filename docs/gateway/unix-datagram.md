# Unix datagram gateway profile

Plain author requests: a `unixgram` listener without return routes needs no
principals, keys or response signing. Plain-profile frames in AGD1 datagrams
run as their author, and the plain response frame is sent in AGD1 datagrams to
the sender's own bound address (an unbound sender gets no reply). The
owner-only socket is the access gate. See [local authors](local-authors.md).

An enabled `unixgram` listener uses the shared protected AGD1 packet engine.
It is distinct from the `unix` stream listener. Configure an absolute socket
path and the operation allowlist, plus exactly one `unixgram` return route for
each configured principal on that listener. The return route names the fixed
response mailbox, recipient key ID and public-key file. Response signing uses
the existing `response_signing_key` configuration. UDP configuration and its
numeric destinations retain their existing contract.

For example, these entries belong in the existing `listeners` and
`return_routes` arrays; the principal and listener operation grants must both
permit the requested operation:

```json
{
  "id": "local-packets",
  "protocol": "unixgram",
  "enabled": true,
  "address": "/private/tmp/gateway-private/ingress",
  "operations": ["send", "reply", "receipt.get", "history.read", "watch", "events.watch"]
}
```

```json
{
  "id": "agent-local-return",
  "principal": "agent",
  "protocol": "unixgram",
  "listener": "local-packets",
  "destination": "/private/tmp/gateway-private/agent-mailbox",
  "recipient_key_id": "agent-response",
  "recipient_public_key_file": "/private/tmp/gateway-private/agent-response-public.pem"
}
```

These paths are Darwin examples; on Linux use an existing native parent such
as `/tmp/gateway-private`. Create an owner-only directory and bind the mailbox
before starting the gateway. Provision the request keys using
[signed-message-v1.md](signed-message-v1.md), and the response signer and
recipient keys using [udp.md](udp.md#owner-configuration). The protected AGD1
wire format is shared with UDP, but the UDP example client does not implement
Unix sockets. The native acceptance group can be reproduced with
`go test -race ./integration/gateway -run '^TestUnixgram' -count=1 -timeout=4m -v`;
it creates its own pinned daemon, accounts, sockets and keys.

Requests carry encrypted detached envelopes in bounded AGD1 fragments.
Response frames are signed and encrypted to the provisioned recipient, with
the realm, principal, binding and return-route ID authenticated in the response
scope. The packet source is neither authority nor a response destination.
Clients may reconnect with a different source socket or use an unnamed source;
they still receive results at the configured mailbox. The mailbox inode is
pinned at startup, so replacing it requires restarting the owned gateway.

Use a private owner-controlled parent directory and `0600` socket files for
the mailbox and ingress. The mailbox must already be bound before startup.
Unsafe parents, symlinks, existing ingress paths, mismatched owners or modes,
and overlong paths are rejected. Ancestors must be owned by the gateway user
or root and deny untrusted writes, except root-owned sticky directories.
Darwin's `/tmp` alias is handled explicitly. Limits are 103 path bytes on
Darwin and 107 on Linux. Every response fragment rechecks mailbox permissions
and inode identity; cleanup removes only the gateway's ingress inode.
These filesystem checks do not isolate mutually hostile processes sharing the
same OS account. Principal isolation comes from protected envelopes and
recipient-bound responses.

Each packet is at most 1000 bytes; larger or truncated input is discarded.
Whole requests retain the 16 KiB frame limit. Plain response JSON is limited
to 32 KiB before protection, with a 64 KiB encrypted wire-frame limit. History
and watch pages use the existing whole-record paging and cursor contract.
Source leases, four partial assemblies per source, five-second assembly/idle
expiry, global admission slots, operation workers and replay caches remain
bounded. Unnamed sources share one bounded assembly bucket. Successful
authentication renews a source lease; invalid input does not.

All fragments of a response share one three-second write deadline and a
serialized writer. Unix sockets can report zero-byte `ENOBUFS` rather than
waiting for a reader. Definite zero-byte buffer-pressure failures retry with a
one-millisecond pause inside that deadline. Partial or uncertain writes are
not retried by this mechanism. Whole watches retain the bounded stream lease
and one-megabyte protected output budget; expiry releases the native session.
For uncertain operation delivery, retry the same signed intent and stable
logical request ID with a fresh transport correlation and AGD1 ID, then query
its receipt. Transport completion is not an AIRC receipt.

## Acceptance

The final Darwin native race group passes in 45.398 seconds against gateway
SHA-256 `65f7f39b02b98ee4895c361b35b9c144db3fd5940ef3e2ef6f87a235deb7ac2b`.
It calls all 33 shared operations against the pinned isolated AIRC fixture,
compares whole history and live records independently, verifies quiet
heartbeats and lease expiry, and joins active watch/partial-input work on
SIGINT before ingress removal and rebinding. Loss/replay uses the same
protected intent with a new source socket and proves one native record and
one send attempt. Invalid input tests cover oversized packets, tampering,
signed-only requests and expired incomplete assemblies with a matched
complete-input control, whose settled receipt and independent native record
are verified. Earlier native groups passed in 43.486s before strengthening
that positive-control assertion and 43.993s before correcting platform-specific
path canonicalization and requiring immediate private-parent ownership.

The initial capabilities timeout was an actual Darwin `ENOBUFS` failure while
writing fragmented output. The bounded retry above resolves it. A native
socket pressure test verifies termination at the whole-write deadline.
Ingress/mailbox lifecycle tests cover permission changes and inode replacement.
[Linux native acceptance](linux-unixgram-testing.md) now covers authenticated
delivery, complete mailbox reads, response-loss replay, watch lifecycle and
private-path boundaries. Device/inode identity checks cannot detect deletion
followed by reuse of the same inode number; recipient-bound encryption remains
required. Windows cross-builds pass; Unixgram is explicitly unavailable on
unsupported OSes.
Vet, staticcheck, formatting and module tidiness pass. The focused service race
tests pass in 1.606s after the path fixes. Earlier full normal/race module
commands exited successfully (integration 1142.649s/1139.022s), but started
before those fixes and are not final-source acceptance. Final full normal/race module
checks both exited successfully against the unchanged 945-file manifest of
the corrected source (integration 1135.372s/1135.949s).

This is partial catalogue row 29 support. The shared gateway operation set
still lacks some native AIRC surfaces, including MONITOR, WHO/WHOIS, persistent
AWAY, NOTICE and typed account-role moderation. Their absence is implementation
work, not a Unix datagram transport limitation. The complete connection catalogue
remains the objective.
