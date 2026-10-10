# Windows named pipes

The chosen implementation is a local Win32 byte-mode named pipe on Windows
AMD64 or ARM64, using Go 1.27 and the existing `golang.org/x/sys/windows`
dependency. `cmd/airc-gateway-pipe` serves one bounded request per invocation
and forwards it to a fixed TLS gateway. It supports signed Send, receipt
recovery and bounded reads through the existing
[framed protocol](framed-stream-v1.md). Streaming watch is rejected.

Framing and upstream TLS forwarding are shared with the [Windows RPC bridge](windows-rpc.md).
They reject duplicate, case-aliased and unknown wrapper fields and require a
complete terminal result/error object. TLS cancellation expires pending I/O
and joins its cancellation callback.

## Accounts, access and identity

Provision a server account, a client account, their canonical numeric user
SIDs, and a pipe name of the form `\\.\pipe\airc-gateway-NAME`; `NAME` contains
1–64 ASCII letters, digits or hyphens. The helper requires its process user
SID to match its configured role. The pipe is local only, rejects remote
clients, permits one instance, and requires first-instance creation. An
existing pipe causes server startup to fail rather than joining that instance.

The explicit security descriptor has the server SID as owner and a protected
DACL with exactly two allow entries: server full access and client mask
`0x00120183` (read/write data and attributes, read control and synchronize).
Client access deliberately excludes `FILE_CREATE_PIPE_INSTANCE`, which shares
the `FILE_APPEND_DATA` bit included in generic write. Both endpoints retrieve
the owner/DACL from their actual open handle and require the expected descriptor
and protected control bit before exchanging application data.

After reading the whole request, the server calls `ImpersonateNamedPipeClient`
on a locked OS thread, reads the thread token's user SID, requires the configured
client SID and calls `RevertToSelf` before forwarding. Failed impersonation or
token inspection rejects the request. Failed reversion terminates the child;
an impersonating thread is never returned to the Go thread pool. The client
requests identification-level security quality of service. No process-ID lookup
or claimed username in the request establishes caller identity.

The account/SID boundary trusts processes running under the provisioned server
and client accounts, along with local administrators and the Windows kernel.
Use dedicated accounts and protect the executable, input files, CA and launch
configuration with normal Windows account permissions. No account, service,
firewall or system policy is installed or changed by this helper.

The original signed or encrypted envelope still supplies the native AIRC
principal, exact grant, timestamp and durable request ID. The local SID is an
additional admission boundary; it never becomes the native author and grants
no authority to mint or rewrite an envelope. Provision the existing gateway's
verification keys, principal/target grants, journal, cursor keys and native
bindings independently.

## Invocation

Build `./cmd/airc-gateway-pipe` for the selected Windows architecture and
provision the executable on Windows. Configure an ordinary `framed` gateway
listener with its certificate/key and enabled `send`, `receipt.get` and desired
bounded-read operations. Its address must be an explicit numeric IP and port;
the bridge verifies the configured CA and certificate name, TLS 1.3 and ALPN
`airc-gateway/1`. TLS is required even for a loopback upstream. The native
gateway may run on a separate supported host.

Run the server under its provisioned account (PowerShell example; replace SIDs,
addresses and paths with actual provisioning values):

```powershell
.\airc-gateway-pipe.exe serve `
  --pipe '\\.\pipe\airc-gateway-main' `
  --server-sid 'S-1-5-21-100-200-300-1001' `
  --client-sid 'S-1-5-21-100-200-300-1002' `
  --address '192.0.2.10:7443' `
  --ca 'C:\AIRC\ca.pem' --server-name 'gateway.example'
```

Within 30 seconds, invoke the client under its configured account:

```powershell
.\airc-gateway-pipe.exe request `
  --pipe '\\.\pipe\airc-gateway-main' `
  --server-sid 'S-1-5-21-100-200-300-1001' `
  --client-sid 'S-1-5-21-100-200-300-1002' `
  --profile signed --correlation request-1 `
  --input 'C:\AIRC\original-request.jws'
```

The input is a bounded regular file containing the compact original signed
envelope, optionally followed by one line ending. Use `--profile encrypted`
for a compact encrypted envelope. The client prints the complete correlated
JSON result or gateway error only after its supervised child exits successfully.
An `event:error` response is a completed protocol exchange, not successful
native Send; callers must inspect the response. Restart both commands for each
request. Underscore-prefixed commands are internal supervisor plumbing.

## Wire, bounds and recovery

The pipe carries a four-byte big-endian length and one framed-v1 JSON request
of 1–16,384 bytes, then a similarly framed result of 1–65,536 bytes. The bridge
forwards the request bytes unchanged. The client validates version, correlation
and terminal event before sending a final framed one-byte `01` consumption
acknowledgement. The server reads that acknowledgement before closing, without
an unbounded `FlushFileBuffers` call. It confirms only consumption of the pipe
reply, never native delivery. A custom client must implement this final frame.

Each child has a 30-second operation context, with five seconds to assemble
the request after connection and three seconds for upstream TLS connection and
handshake. Pipe opening retries only absence/busy errors in 50-ms intervals.
Each OVERLAPPED operation owns an event and joins completion before releasing
its buffers. Cancellation requests `CancelIoEx` and joins the pending operation;
native cancellation can itself stall. The parent supervises the child for
45 seconds, kills it on expiry/interruption and waits for it to exit. A forced
termination reports an uncertain outcome, not successful native cleanup.

The bridge has one connection, bounded native pipe buffers and one whole
request/result buffer. It rejects over-limit frames, indefinite watch, malformed
results and mismatched correlation. The gateway independently checks original
intent freshness, signatures, grants, durable replay and bounded page/cursor
semantics. Pipe connection, write completion or helper exit alone does not prove
AIRC acceptance. After a lost result, recover using the original signed mutation
and durable request ID; do not regenerate its timestamp or identity.

## Deferred verification

Source review and offline Windows AMD64/ARM64 production cross-builds are the
current evidence. No Windows helper, pipe, token, TLS exchange or native AIRC
delivery was executed. Later acceptance requires real Windows account fixtures,
independent AIRC observation, wrong-SID/owner/DACL and pre-created-instance
refusals, remote-client rejection, whole bounded reads, original replay,
truncated/oversized frames, partial I/O, lost acknowledgements and interruption
through pending native I/O. Cross-compilation does not qualify those behaviors.

Primary references: Microsoft [named-pipe access rights](https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights),
[client impersonation](https://learn.microsoft.com/en-us/windows/win32/api/namedpipeapi/nf-namedpipeapi-impersonatenamedpipeclient),
[handle security inspection](https://learn.microsoft.com/en-us/windows/win32/api/aclapi/nf-aclapi-getsecurityinfo)
and [pending I/O cancellation](https://learn.microsoft.com/en-us/windows/win32/fileio/canceling-pending-i-o-operations).
