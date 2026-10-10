# Embedded XPC bridge

The Darwin ARM64 XPC bridge forwards encrypted unary requests to an existing
[`mach_ipc` listener](mach-ipc.md). A native client talks to an embedded XPC
service in its application bundle; that service forwards the exact request
bytes over Mach and returns the opaque protected response. The Go gateway
continues to own authentication, admission, durable replay and AIRC delivery.

This is the app-bundled XPCService profile. It needs no installed LaunchAgent or
LaunchDaemon, although launchd manages the embedded service process on demand.
Apple describes this packaging in its
[XPC service guide](https://developer.apple.com/documentation/xpc/creating-xpc-services).
Named system services, independently installed launchd jobs, sandboxed clients
and cross-user service access are separate deployment profiles.

## Build and use

Provision the Mach listener and its UID/principal mappings, encrypted ingress
keys and protected return routes as described in [mach-ipc.md](mach-ipc.md).
The upstream bootstrap name is fixed in the bridge's own `Info.plist`; an XPC
request cannot select another destination.

Build a fresh application bundle using the existing Apple SDK and ad-hoc code
signing:

```sh
./scripts/build-xpc-bridge.sh \
  --output /absolute/path/GatewayXPC.app \
  --mach-service org.example.airc.gateway
```

The output path must not already exist. The builder creates the client and
embedded service, signs them and verifies the bundle. It does not install the
bundle or modify launchd configuration. To submit a retained encrypted
`FrameRequest`:

```sh
/absolute/path/GatewayXPC.app/Contents/MacOS/client \
  < retained-frame.json > sealed-response.txt
```

The client writes only the opaque response token to standard output. Authenticate
and decrypt that token using the configured Mach return route, then verify the
exact correlation ID, version and sequence. A successful XPC exchange alone is
not an AIRC receipt: inspect durable admission and use `receipt.get` to establish
native acceptance.

## Caller mapping and wire contract

The service reads the peer EUID from the live XPC connection and requires it to
equal the service's effective UID before forwarding. The Mach kernel then audits
that same service UID at the gateway. Consequently the existing Mach UID and
authenticated signing-principal pair identifies the original XPC caller at the
UID level. There is no request-supplied UID or principal field and no additional
signing key, decryption step or replay store in the bridge.

Mach observes the bridge's PID, not the XPC client's PID. PID remains diagnostic
and never grants authority. Direct Mach requests permitted by the underlying
listener remain permitted; XPC does not add a separate authorization policy.

| Direction | XPC dictionary |
|---|---|
| Request | Exactly one `frame` data value containing 1–16,384 bytes |
| Success | Exactly one `response` data value containing the protected token, at most 65,536 bytes |
| Transport failure | Exactly one bounded `error` string; no success receipt |

The bridge does not alter frame bytes, envelope, correlation ID or response
token. The upstream [Mach wire contract](mach-ipc.md#native-wire-contract)
continues to apply, including refusal of streaming requests. Each history call
returns a whole bounded page; advance its opaque cursor only after verifying
the complete protected reply, and continue through the empty caught-up page.

## Retry and lifetime

A lost client connection or response can leave an already admitted request.
Retain and retry the same signed intent and durable request ID within its
freshness window. The gateway journal supplies the retained result across new
client and embedded-service processes.

The client option `--drop-reply` receives and discards a response without writing
its token. This models a caller losing its receipt; it does not claim that the
XPC transport failed before delivery.

Each embedded service accepts one live peer and one request on that connection.
Its event handler forwards serially. Mach sends and receives share a four-second
deadline; the public bootstrap lookup is synchronous and cannot be canceled.
The CLI has an eight-second process alarm covering input, the XPC reply and
output, and preserves the parent's shared file-descriptor flags.

These application limits do not bound XPC's own decoding allocation before its
event handler receives an object. The gateway's shared request limit and Mach
queue continue to bound admitted work. Service-process loss releases its Mach
rights; a gateway shutdown joins its admitted work and destroys its owned
receive port.

## Native qualification

On Darwin ARM64, the integration fixture builds and directly invokes the actual
ad-hoc signed application bundle, including its separate XPC service. Native C
code runs with strict compiler warnings, AddressSanitizer and UndefinedBehaviorSanitizer;
sanitizer failures terminate the process. An independent native AIRC observer
checks the author, room and exact Unicode message bodies. Cursor reads collect
the entire retained history through its empty terminal page and match native
history; a single larger protected reply exceeds 16 KiB.

The focused checks cover retry after a discarded receipt with one native send,
expired and unauthenticated envelopes, wrong audience, UID/principal mapping
mismatches, malformed XPC dictionaries and oversized input. Refused calls leave
no journal admission or native post, and a following valid call succeeds. A
blocked-input client retains the parent's descriptor flags and terminates on
interrupt or its own deadline. Shutdown joins the gateway, releases the journal
and lets the embedded service exit after its client disconnects.

A native queue-full check exercises the actual four-second Mach send timeout.
It verifies that the transaction leaves the process's port-name set and original
service send/receive reference counts unchanged. The check first reproduced a
leaked dead-name right, then passed after explicit disposal of the local
send-once right returned by a failed send. Apple's
[unsent-message cleanup](https://github.com/apple-oss-distributions/xnu/blob/main/libsyscall/mach/mach_msg.c)
likewise releases that local right before `mach_msg_destroy`, which does not
dispose it.

The shutdown fixture resumes a deliberately stopped gateway before sending its
interrupt. On the tested Go 1.27.1 / Darwin ARM64 host, sending SIGINT while a
process is stopped and then SIGCONT also hangs a plain Go `signal.NotifyContext`
control with no Mach code (three of three delayed trials). Resuming before
SIGINT exits cleanly in all three controls. That pending-signal order is outside
the shutdown claim; it does not establish a Mach-specific defect.

Full normal/race, build, formatting, vet and staticcheck acceptance passed on
unchanged source. Exact commands, source/archive digests and terminal results
are in the [XPC acceptance checkpoint](testing.md#xpc-acceptance-checkpoint).
Foreign-UID processes, sandboxed clients, independently installed services and
cross-login namespaces remain unqualified.
