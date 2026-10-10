# Windows RPC: local ncalrpc bridge

The selected API is Windows 10 / Server 2016 or later desktop `ncalrpc`, on
AMD64 or ARM64. Windows SDK MIDL and two native C workers carry one bounded
exchange. The Go `airc-gateway-rpc` supervisor forwards its original framed
request to a fixed TLS gateway. Send, receipt recovery and bounded reads use
the existing signatures, grants, freshness and journal rules. Watch is rejected.

## Identity and wire contract

Provision dedicated server/client accounts and their canonical numeric user
SIDs. Each worker requires its process token to match its role. The server
registers only this local interface, with a protected interface DACL owned by
the server and granting access only to those two SIDs. Its security callback
requires WINNT authentication and packet privacy, impersonates the caller,
compares `TokenUser` with the exact client SID and reverts before returning.
The callback runs before argument unmarshalling; the method checks the caller
again. Failed reversion immediately terminates the worker.

The client uses a partial local binding, QOS version 3 with the expected server
SID, static identity, identification-level impersonation and
`LOCAL_MA_HINT | MUTUAL_AUTH`. The server registers its dynamic local endpoint
with the Windows endpoint mapper. Authentication is configured before lookup,
so the documented local hint filters registrations to that server principal.
This relies on the local RPC runtime and endpoint mapper; a generic NTLM
mutual-authentication flag alone does not provide the same guarantee. Fixed
endpoints and prior unauthenticated resolution are outside this profile.

Run one instance per server SID and interface. Processes under the provisioned
accounts, local administrators, Windows runtime and kernel are trusted. The
interface ID is `5b82e647-45c6-4c43-9359-2c97a440f225`, version 1.0. Its single
explicit-handle `Exchange` accepts 1–16,384 bytes and returns at most 65,536.
An atomic gate admits one method invocation. Normal cleanup unregisters the
endpoint/interface and waits for outstanding calls. The runtime also reclaims
dynamic endpoint registrations after process exit.

The original signed or encrypted envelope establishes the AIRC author and
authority. The Windows SID is an additional admission check; it never replaces
or rewrites the native principal, signature, target, timestamp or request ID.
Upstream TLS requires an explicit numeric IP/port, provisioned CA and certificate
name, TLS 1.3 and ALPN `airc-gateway/1`, including for a loopback gateway.

## Build and invocation

In a provisioned Visual Studio environment with MSVC, Windows SDK MIDL and
CMake 3.25+, build the workers. These commands are for later execution; they
were not run in the implementation environment:

```powershell
cmake -S native/windows-rpc -B C:\AIRC\rpc-build -A x64
cmake --build C:\AIRC\rpc-build --config Release
go build -o C:\AIRC\airc-gateway-rpc.exe ./cmd/airc-gateway-rpc
```

Use `-A ARM64` and the matching Go target for ARM64. CMake requires an explicit
Visual Studio architecture, robust MIDL stubs, C11 and strict handwritten-C
warnings. Generated source stays in the build directory. Provision executable
SHA-256 pins through the operator's trusted build/distribution process.

Protect executables, their parent directories, dependencies, input, CA and
launch configuration from untrusted modification. The supervisor requires a
canonical local drive path without reparse-point aliases, an architecture-matched
PE executable of at most 16 MiB and its exact lowercase SHA-256 pin. It holds
the image open without write/delete sharing until child exit. Ancestor-directory
permissions and the provisioned DLL environment remain operator trust requirements;
the digest is not automatic vendor-signature verification. No installation,
accounts, firewall changes or persistent service registration are performed.

Configure an ordinary TLS `framed` gateway with the required native bindings,
verification keys, principal/target grants, journal/cursor keys and operations.
It may run on another supported host. Start under the server account, replacing
all example values with actual provisioning:

```powershell
.\airc-gateway-rpc.exe serve `
  --worker 'C:\AIRC\rpc-build\Release\airc-rpc-server.exe' `
  --sha256 SERVER_WORKER_SHA256 `
  --server-sid 'S-1-5-21-100-200-300-1001' `
  --client-sid 'S-1-5-21-100-200-300-1002' `
  --address '192.0.2.10:7443' --ca 'C:\AIRC\ca.pem' `
  --server-name 'gateway.example'
```

Wait for the readiness message on stderr, then invoke under the client account
within the server's 30-second window:

```powershell
.\airc-gateway-rpc.exe request `
  --worker 'C:\AIRC\rpc-build\Release\airc-rpc-client.exe' `
  --sha256 CLIENT_WORKER_SHA256 `
  --server-sid 'S-1-5-21-100-200-300-1001' `
  --client-sid 'S-1-5-21-100-200-300-1002' `
  --profile signed --correlation request-1 `
  --input 'C:\AIRC\original-request.jws'
```

Use `--profile encrypted` for an original compact encrypted envelope. The client
prints the whole correlated terminal JSON response after worker success/cleanup.
Inspect its native result: `event:error` is a completed exchange, not an accepted
Send. Restart both commands for another request. Native executables are internal
supervisor workers requiring pipe-backed stdin/stdout.

## Bounds, lifetime and recovery

MIDL range checks bound input size and output varying length; the output array
has a fixed 65,536-byte capacity. `/robust` enables those checks. The allocator
caps each allocation including its header at 128 KiB and aggregate outstanding
stub allocations at 512 KiB. `RpcServerRegisterIf3.MaxRpcSize` does **not** bound
`ncalrpc`; the implementation does not rely on it. Listener concurrency is
best-effort, supplemented by the method's atomic single-call guard.

Before stdin/RPC processing, the worker joins a Windows job with one-process
and 64 MiB committed-memory limits; failed setup rejects startup. A native
35-second watchdog covers calls and cleanup. The server waits 30 seconds, the
client requests a 20-second RPC call timeout, and the Go parent supervises for
35 seconds, kills on expiry/interruption and waits for exit. The watchdog also
bounds a worker after abrupt parent death; cleanup is not instantaneous. Forced
termination reports uncertainty, not successful native cleanup.

Private parent/worker records use four-byte big-endian lengths. Server stdout
emits readiness (`length=1, byte=01`), then one framed-v1 request; stdin receives
one result. Client stdin receives a request and stdout emits its result. The
parent rejects trailing output and waits for successful child exit before
releasing a client result. Shared framing rejects duplicate/case-aliased/unknown
wrapper fields, malformed JSON, streaming requests, invalid terminal payloads
and correlation mismatch. TLS setup is bounded to three seconds; cancellation
expires I/O deadlines and joins its callback.

RPC completion or helper exit is not independent proof of native AIRC acceptance.
After result loss, retain the original signed mutation and recover its durable
receipt; do not generate a new timestamp or identity to retry.

## Deferred verification

The Go supervisor and shared named-pipe integration pass offline Windows
AMD64/ARM64 production cross-builds. Native MIDL/C has source review only:
Windows SDK `midl`/`cl` is unavailable here, so its first native build remains
required. No RPC process, endpoint registration, job, token, TLS exchange or
AIRC request was run. No runtime acceptance is claimed.

Later acceptance must build the workers, then verify principal-filtered endpoint
lookup, interface DACLs, wrong-account refusals, packet privacy, malformed NDR,
one-call concurrency, Send/read/receipt behavior, original replay, lost replies,
memory bounds and joined/forced shutdown including supervisor death. Use an
independent AIRC observer for author, target and body.

Primary references: Microsoft [QOS v3 SID/local hint](https://learn.microsoft.com/en-us/windows/win32/api/rpcdce/ns-rpcdce-rpc_security_qos_v3_w),
[binding authentication](https://learn.microsoft.com/en-us/windows/win32/api/rpcdce/nf-rpcdce-rpcbindingsetauthinfoexw),
[endpoint registration](https://learn.microsoft.com/en-us/windows/win32/api/rpcdce/nf-rpcdce-rpcepregister),
[interface security and size semantics](https://learn.microsoft.com/en-us/windows/win32/api/rpcdce/nf-rpcdce-rpcserverregisterif3)
and [MIDL range checks](https://learn.microsoft.com/en-us/windows/win32/midl/range).
