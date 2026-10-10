# Windows COM: one local IDispatch exchange

The selected implementation uses Windows 10 / Server 2016 or later desktop COM
on AMD64 or ARM64. Two native C workers marshal a standard `IDispatch` object
between processes and invoke one `Exchange` method. The Go
`airc-gateway-com` supervisor forwards its original framed request to a fixed
TLS gateway. Send, receipt recovery and bounded reads retain the existing
signature, grant, freshness and durable journal rules. Watch is rejected.

## Identity and object bootstrap

Provision dedicated server/client accounts, their canonical numeric user SIDs,
and a local pipe name `\\.\pipe\airc-gateway-NAME`, with 1–64 ASCII letters,
digits or hyphens in `NAME`. The [named-pipe implementation](windows-named-pipes.md)
verifies owner/DACL on both open handles, rejects remote clients, requires the
role's process SID and checks the caller token after reading its frame.

That pipe carries the COM object reference and lifecycle acknowledgements.
The application request and result travel through the actual out-of-process
COM `IDispatch::Invoke` call. No class/typelib/proxy DLL registration, COM
activation service installation or registry mutation is needed. The object uses
the stock `IDispatch` marshaler, not an arbitrary custom marshaler.

The server marshals `IID_IDispatch` with `MSHCTX_LOCAL` and
`MSHLFLAGS_TABLESTRONG`, retains its stream and explicitly releases marshal data
at shutdown. The reference is bounded to 4 KiB and its standard OBJREF header
and interface ID are checked. The client unmarshals it only after receiving it
through the authenticated pipe. Untrusted files or caller-supplied arbitrary
marshal packets must never be used as bootstrap references.

COM security uses explicit account access permissions and WINNT packet privacy.
Before admitting `Invoke`, the server requires local LRPC call attributes,
rejects unknown/remote/anonymous context, impersonates the caller and checks the
exact configured client SID. Reversion failure terminates the worker. This
runtime check establishes local admission; `MSHCTX_LOCAL` alone is not claimed
to prove the actual transport. The client requests identification-level
impersonation and packet privacy on its proxy.

The server identity proof is the SID-verified bootstrap pipe and its pinned
server worker, which runs under that SID. `CoSetProxyBlanket` does not bind the
proxy to a numeric SID; its principal-name parameter is not used as a substitute.

The account boundary trusts processes under the provisioned accounts, local
administrators and Windows COM/RPC/kernel services. The original signed or
encrypted envelope supplies the AIRC author and authority; no Windows username
or SID is substituted for the native principal, target or signature.

## Build and invocation

In a provisioned Visual Studio environment with Windows SDK, MSVC and CMake
3.25+, build the native workers and Go supervisor. These are later execution
instructions, not commands run during implementation:

```powershell
cmake -S native/windows-com -B C:\AIRC\com-build -A x64
cmake --build C:\AIRC\com-build --config Release
go build -o C:\AIRC\airc-gateway-com.exe ./cmd/airc-gateway-com
```

Use `-A ARM64` and a matching Go target for ARM64. Provision the native executable
SHA-256 pins through a trusted build/distribution process. The shared
[worker image policy](windows-rpc.md#build-and-invocation) requires canonical
local drive paths, architecture-matched executables at most 16 MiB, exact pins
and handles kept open without write/delete sharing. Protect ancestor directories,
DLL dependencies, input, CA and launch configuration with Windows permissions.

Configure an ordinary TLS-enabled framed gateway, possibly on another supported
host, with the native bindings, verification keys, exact grants, journal/cursor
keys and desired operations. TLS 1.3, the provisioned CA/certificate name and
ALPN `airc-gateway/1` are required even for loopback. Start under the server
account, substituting actual provisioning values:

```powershell
.\airc-gateway-com.exe serve `
  --pipe '\\.\pipe\airc-gateway-com' `
  --worker 'C:\AIRC\com-build\Release\airc-com-server.exe' `
  --sha256 SERVER_WORKER_SHA256 `
  --server-sid 'S-1-5-21-100-200-300-1001' `
  --client-sid 'S-1-5-21-100-200-300-1002' `
  --address '192.0.2.10:7443' --ca 'C:\AIRC\ca.pem' `
  --server-name 'gateway.example'
```

Once the server reports that its object is ready, invoke under the client
account promptly within the bounded server lifetime:

```powershell
.\airc-gateway-com.exe request `
  --pipe '\\.\pipe\airc-gateway-com' `
  --worker 'C:\AIRC\com-build\Release\airc-com-client.exe' `
  --sha256 CLIENT_WORKER_SHA256 `
  --server-sid 'S-1-5-21-100-200-300-1001' `
  --client-sid 'S-1-5-21-100-200-300-1002' `
  --profile signed --correlation request-1 `
  --input 'C:\AIRC\original-request.jws'
```

Use `--profile encrypted` for a compact encrypted envelope. The complete
correlated JSON result is printed after both native workers complete their
normal cleanup and the client sends its final pipe acknowledgement. Inspect the
terminal event and native result: an `event:error` is a completed exchange,
not accepted Send. Restart both commands for another request. Underscore-prefixed
commands and native executables are internal supervisor plumbing.

## Bounds and lifecycle

`Exchange` is DISPID 1, `DISPATCH_METHOD`, one positional `VT_BSTR` request and
a `VT_BSTR` result. No by-reference variants, named arguments or other methods
are accepted. Strict UTF-8/UTF-16 conversions preserve the original JSON bytes,
reject invalid encodings/NULs and enforce 16 KiB requests and 64 KiB results.
An atomic guard admits one invocation. BSTR unmarshalling happens before the
method body: its per-message checks do not bound that earlier allocation. Each
native worker instead has a one-process job and 64 MiB committed-memory limit,
plus a 35-second watchdog. Job setup failure rejects startup.

The native server has a 30-second invocation wait. The Go coordinator has a
35-second operation context and kills/joins its native worker on normal error
or interruption. An outer Go supervisor imposes 45 seconds, covering a stalled
Windows pipe cancellation/completion join. Abrupt coordinator death or forced
outer termination can leave a native child until its independent watchdog fires;
this is bounded recovery, not an instantaneous joined shutdown claim.

Dispatch owns its SID copy and completion handle. Normal shutdown stops admission
and drains active method bodies before releasing the table reference. Failure
after publishing the object terminates the worker, avoiding teardown of state
that a pending COM call could still use.

All control records use four-byte big-endian lengths. On the authenticated
bootstrap pipe: client `01`, server OBJREF, client `01` consumption acknowledgement.
After receiving its COM result, releasing its proxy and exiting successfully,
the client worker allows its Go coordinator to send `02`. The server coordinator
then sends `02` to its worker, permitting object disconnection, marshal-data
release and COM uninitialization. After server worker success, pipe `03` confirms
cleanup; client `04` confirms consumption before closure. These acknowledgements
are lifecycle evidence, not additional native AIRC receipts.

Server worker stdout carries OBJREF then the COM request; stdin carries result
then `02`. Client worker stdin carries trusted OBJREF then request; stdout
carries result. The parent rejects trailing output and requires successful
native exit. This sequence keeps the server alive until the client has received
the marshaled result; a method-body completion event alone would not prove that.

After an uncertain result, recover using the original signed mutation and
durable request ID. Do not regenerate its timestamp or identity to retry.

## Deferred verification

The Go integration is cross-built; native C has source review only. No Windows
SDK/MSVC environment is available here, so the first native worker build is
still required. No COM process, object marshal, token check, pipe, TLS exchange
or AIRC request was run, and no runtime acceptance is claimed.

Later acceptance must build the workers and verify stock IDispatch marshalling,
actual local LRPC attributes, interface access and wrong-SID refusals, packet
privacy, malformed variants/OBJREFs, memory limits, complete Send/read/receipt
behavior, replay, proxy loss, each acknowledgement boundary and interrupted or
forced shutdown. Use an independent AIRC observer for author, target and body.

Primary references: Microsoft [COM process security](https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-coinitializesecurity),
[trusted unmarshalling](https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-counmarshalinterface),
[RPC call attributes](https://learn.microsoft.com/en-us/windows/win32/api/rpcasync/ns-rpcasync-rpc_call_attributes_v2_w),
[object disconnection](https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-codisconnectobject)
and [marshal-data release](https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-coreleasemarshaldata).
