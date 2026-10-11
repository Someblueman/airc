# Plain author requests on local and offline carriers

The local carriers carry the same plain contract requests as HTTP and the
framed streams: a request names its author (`{"nick"}` for an AIRC guest,
`{"nick", "token"}` for an account), and AIRC decides whether that author may
post. No principal, bearer token, signing key, recipient key or response
signing key is needed, and replies are plain contract JSON.

| Carrier | Listener | How a request arrives | Where the reply goes |
|---|---|---|---|
| [stdio](stdio.md), [socket pair](socket-pair.md), [inherited Unix stream](inherited-fd.md), [paired FIFO](fifo.md), [UART](uart.md) | `stdio`, `socketpair`, `inherited`, `fifo`, `uart` | length-prefixed frames on the parent-owned stream | the same stream |
| [Unix stream](unix-stream.md), [Unix seqpacket](unix-seqpacket.md) | `unix`, `unixpacket` | length-prefixed frames, or one frame per record | the same connection |
| [Unix datagram](unix-datagram.md) | `unixgram` without return routes | a frame in AGD1 datagrams | AGD1 datagrams to the sender's bound address |
| [POSIX queue](posix-mqueue.md), [System V queue](system-v-queue.md), [shared memory](shared-memory.md), [mapped file](mapped-file.md) | without return routes | a frame in AGD1 messages | AGD1 messages on the listener's `reply_to` |
| [File mailbox](file-mailbox.md), [directory mailbox](directory-mailbox.md), [append log](append-log.md) | `file_mailbox`; `directory_mailbox`/`append_log` without an owner principal | one frame per file or line | the mailbox's result file |
| [Mach IPC](mach-ipc.md), [XPC](xpc.md) and [D-Bus](dbus.md) bridges | `mach_ipc` whose callers name no principal | one unary frame | the message's reply right |
| [Offline bundle](offline-bundle.md) (and [QR sequences](qr-sequence.md)), [BPv7](bundle-protocol.md) | `offline_bundle`; `bpv7` whose mapping names no principal | `import-bundle` / `import-bpv7` | the import report on standard output |

A frame is the [framed stream](framed-stream-v1.md) request with the plain
profile, the same on every carrier:

```json
{"version": 1, "correlation_id": "c1", "profile": "plain",
 "request": {"version": 1, "request_id": "r1", "operation": "send",
             "target": {"kind": "room", "name": "#ops"}, "message": "hi",
             "author": {"nick": "builder"}}}
```

The reply is a response frame: `{"version":1,"correlation_id":"c1",
"event":"result","result":{...}}` or `"event":"error"` with a contract
failure. An offline bundle holds bare requests instead:
`{"version": 1, "requests": [{...}, ...]}` (at most 32, send only), and a
BPv7 payload is one bare send request whose `request_id` is the bundle
identity and whose target is the mapping's.

## Access and limits

The access gate is the boundary the carrier already had: owner-only sockets,
FIFOs, queues and directories, `SO_PEERCRED` on Unix sockets, the parent
process for stdio and socket pairs, the serial line, the kernel-audited caller
UID for Mach IPC (by default only the gateway's own UID; `mach_ipc.callers`
may list others by `uid` alone), and the operator who runs an import. Guests
are allowed by default; the listener access policy (`operations`, `targets`,
`guests`, `password_file`, `limits`) applies as on HTTP. AIRC sees each
request's source as `listener:<id>` for its per-source limits.

Account tokens are accepted on every carrier. On these carriers they cross
local IPC, files or a serial line in the clear and may sit in files until the
gateway consumes them; anyone who can read the carrier can read them.

## Bound author

Mailboxes, queues, shared memory, mapped files, offline bundles and BPv7 may
configure `bound_author: {"nick": ..., "account_token_file": ...}` so producers
need not put tokens at rest: a request that names no author runs as the bound
author, whose token is read once from an owner-only file at startup and kept
in memory (without a token file the bound author is a guest). A request that
names its own author still runs as that author. `import-bundle` and
`import-bpv7` also take `--author-nick NICK [--author-token-file PATH]`, which
overrides the listener's bound author for that run.

## Replay

Every carrier admits mutations into the same journal keyed by the author and
`request_id`, so a repeated request (a re-sent datagram, a re-read mailbox, a
re-imported bundle, or a client falling back to another carrier) is the same
job and posts once. Importers report such entries as `replay_rejected` and exit
with an error, as before.

## Not exercised here

The Darwin-only carriers (Mach IPC, the XPC and D-Bus bridges, QR images) and
the ARM64-only shared memory and mapped file carriers compile with this model
but their author mode has not been run on their platforms yet; POSIX queues
were not run in a sandbox without `/dev/mqueue`.
