# Mach IPC gateway

The `mach_ipc` listener provides encrypted unary requests on Darwin ARM64.
It registers a configured name in the process's existing Mach bootstrap
namespace. The gateway uses the public Mach and BSM APIs through the existing
Go system-library trampoline convention; it requires no cgo worker or installed
launchd job. Other targets reject an enabled listener.

## Configuration and caller identity

Add a listener to an otherwise provisioned gateway configuration:

```json
{
  "id": "local-mach",
  "protocol": "mach_ipc",
  "enabled": true,
  "address": "org.example.airc.gateway",
  "operations": ["send", "receipt.get", "history.read"],
  "mach_ipc": {
    "callers": [{"uid": 501, "principal": "writer"}]
  }
}
```

Use the caller's actual effective UID. The UID field is required; UID zero is
an explicit grant, not a default. A caller mapping contains an exact UID and
configured signing principal. One UID may have several separately authenticated
principals, and a principal may be granted to several UIDs. Duplicate pairs
are invalid. The listener permits at most 1,024 pairs.

The receiver requests and validates the kernel's audit trailer and obtains its
effective UID through `audit_token_to_euid`. It checks that UID before envelope
cryptography, authenticates the encrypted signed envelope, then checks the exact
UID/principal pair before admission. The audit PID is diagnostic metadata, not
an identity or replay key. Caller-supplied payload fields, bootstrap names and
port names cannot choose an AIRC identity.

Provision the existing encrypted ingress keys and response signing key, and
exactly one protected return route for each mapped principal:

```json
{
  "id": "mach-writer-reply",
  "principal": "writer",
  "protocol": "mach_ipc",
  "listener": "local-mach",
  "destination": "reply_port",
  "recipient_key_id": "reader",
  "recipient_public_key_file": "/private/reader-public.pem"
}
```

The verified principal and binding select the recipient key. The request's
header provides only a one-use reply destination. Replies remain encrypted even
if the caller transfers a reply right belonging to another process. TLS,
anonymous access and browser-origin options do not apply.

## Native wire contract

Use a simple Mach message with a 24-byte `mach_msg_header_t`, followed by a
native little-endian `uint32_t` payload length, the exact payload bytes, and
zero padding to a four-byte boundary. `msgh_size` includes this padding and
excludes the kernel trailer. Complex descriptors and vouchers are rejected.

| Field | Request | Reply |
|---|---|---|
| Message ID | `0x41495201` | `0x41495202` |
| Payload | UTF-8 `FrameRequest` JSON | Compact encrypted response token |
| Payload limit | 16 KiB | 64 KiB |
| Sending remote disposition | `MACH_MSG_TYPE_COPY_SEND` | `MACH_MSG_TYPE_MOVE_SEND_ONCE` |
| Sending local disposition | `MACH_MSG_TYPE_MAKE_SEND_ONCE` | Zero |

The client obtains the service send right through `bootstrap_look_up` and
allocates a fresh receive port for each request. Include room for both the
maximum message and the SDK's maximum trailer in receive buffers. The
independent native client source is
[`mach_ipc_client.c`](../../integration/gateway/testdata/mach_ipc_client.c).

The JSON request has `version: 1`, `profile: "encrypted"`, an `envelope`, and
`correlation_id` equal to the 64-character lowercase SHA-256 digest of the
exact envelope token bytes. `stream: true` is refused before intake: a
send-once right carries one reply. Verify the response signature, recipient,
lifetime and exact realm/principal/binding/route scope using the
[protected response format](udp.md), then require the original correlation,
`version: 1` and `sequence: 1` in the decrypted `FrameResponse`.

Malformed or unauthenticated requests and caller-map mismatches receive no
application reply. Authenticated admission failures return a protected error.
A successful Mach send proves transport submission only. Use the durable
receipt and `receipt.get` to establish AIRC acceptance.

## Reads, retries and resource lifetime

History reads return whole bounded pages. Verify each complete response before
advancing its opaque cursor; use a fresh request/reply port for the next page
and continue through the empty caught-up page. A page too large for one
protected reply produces a capacity error rather than a partial page. This
profile advertises no streaming watch mode.

If a reply is lost, retry the retained signed intent with the same durable
request ID while its freshness window remains valid. The existing journal
preserves accepted receipts and replay outcomes across gateway restarts;
neither the Mach port nor the audit PID creates a new replay namespace.

The native ingress queue limit is five messages. Authentication runs serially,
and the shared request limit bounds dispatched work and outstanding reply
rights. Requests have a four-second deadline; reply writes have at most three
seconds within it. Receive polls are bounded to 100 ms. Shutdown closes the
owned receive right and joins admitted work; every rejected or abandoned
request releases its reply right. A duplicate bootstrap registration fails
without replacing the current owner.

Oversized messages are received without `MACH_RCV_LARGE`, so they are discarded
instead of remaining at the head of the queue. Complex messages are immediately
destroyed, including transferred rights and out-of-line memory. These are
application message and queue bounds: Mach can map out-of-line memory before
the receiver can inspect and reject a complex message, so they do not impose
an absolute bound on kernel or transient mapped memory. See Apple's
[receive implementation](https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/ipc/mach_msg.c).

The public `bootstrap_register` API is deprecated but available on the tested
host. The profile requires processes in a compatible bootstrap namespace;
system-daemon, sandboxed-app, cross-login-session and XPC behavior require
separate qualification. No XPC support is implied.

## Native qualification

The native fixture runs a separate C client compiled against the Apple SDK with
strict C11 warnings, AddressSanitizer and UBSan. It verifies encrypted sends
through an independent AIRC observer, comparing author, target and Unicode
body; whole one-message cursor pages through the empty caught-up page; a sealed
history reply larger than 16 KiB; and exact-envelope retry after abandoning a
reply, with one native message and one send attempt.

Refusal cases exercise malformed, signed-profile, expired, wrong-audience,
correlation, stream, oversized and caller-map failures without journal or native
intake. A real request succeeds after oversized-message rejection. Caller-map
denial uses a mismatched configured UID and a separately configured principal;
the fixture does not launch a process under another operating-system UID.
The adapter's capacity test fills the shared request slot, verifies a protected
error and absent journal entry, then retries the same frame successfully after
releasing capacity.

Native package tests check kernel audit UID/PID from a child process, exact
16 KiB request and 64 KiB reply limits, both application and kernel oversize
paths, complex port-right cleanup, dead and cancelled reply handling, and
bounded receive cancellation. Service tests check duplicate-name preservation,
startup rollback and joined shutdown. The process-level shutdown test also
verifies journal release and bootstrap lookup failure after exit.

The qualification host is macOS 26.6.2 / Darwin 25.6.0 ARM64, using Go 1.27.1
and the macOS 27.0 SDK. Full normal/race, build, formatting, vet and staticcheck
acceptance passed on unchanged source. Exact commands, source/archive digests
and terminal results are in the
[Mach IPC acceptance checkpoint](testing.md#mach-ipc-acceptance-checkpoint).
