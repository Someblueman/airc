# Hyper-V sockets: guest TLS through a fixed host relay

The selected profile uses a Windows 10 / Server 2016 or later Hyper-V host and
Windows guest, on AMD64 or ARM64. `airc-gateway-hvsock` uses native WinSock
`AF_HYPERV`, `SOCK_STREAM`, `HV_PROTOCOL_RAW`. The host binds one exact guest VM
ID and one provisioned service GUID. The guest connects to `HV_GUID_PARENT` and
that service, then performs an ordinary framed-v1 TLS exchange with the gateway.

TLS is end to end between the guest and the existing AIRC gateway. The host
relays ciphertext to a fixed numeric TCP endpoint and holds no TLS private key
or native signing credential. The guest verifies its CA, certificate name and
ALPN `airc-gateway/1`, with TLS 1.3 required. Original signed/encrypted envelopes,
author mapping, exact grants, freshness, replay and native durable receipts are
unchanged. Send, receipt recovery and bounded whole reads are supported by the
client command; streaming requests are rejected there.

This is a separate concrete mode from [Linux virtio-vsock](virtio-vsock.md).
Linux guests' VSOCK-to-service-GUID mapping, containers and wildcard partition
addresses are not part of this profile.

## Provisioning and invocation

Register an operator-selected custom service GUID in the host's
`HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Virtualization\GuestCommunicationServices`
registry namespace using the authorized Hyper-V administration process. This
is a host administrator prerequisite, not an action performed by this command.
The guest VM must actually exist and support Hyper-V sockets. Obtain its real
ID from the host's VM configuration; a guest hostname is not its VM ID.

Both IDs must be canonical lowercase GUIDs. Zero, broadcast, parent, children,
loopback and silo-host reserved IDs are rejected as configured guest/service
IDs; the VSOCK service template namespace is also rejected. The guest's parent
destination is fixed by the implementation, not supplied as a caller-selected
partition. The command does not create VMs, mutate registry, change firewall or
network policy, install a service, or acquire privileges.

Build the Go command on each Windows endpoint, with the repository's existing
toolchain and dependencies:

```powershell
go build -o airc-gateway-hvsock.exe ./cmd/airc-gateway-hvsock
```

On the host, substitute the actual guest/service IDs and gateway endpoint:

```powershell
.\airc-gateway-hvsock.exe serve `
  --guest-vm '11111111-2222-4333-8444-555555555555' `
  --service '6f623449-9db0-4e5d-a0f4-9476b2bd3c25' `
  --address '192.0.2.10:7443'
```

The address must be a numeric unicast IP with a nonzero port; hostnames and
client-supplied relay destinations are refused. Once it reports that it is
waiting, promptly run in the guest:

```powershell
.\airc-gateway-hvsock.exe request `
  --service '6f623449-9db0-4e5d-a0f4-9476b2bd3c25' `
  --ca 'C:\AIRC\ca.pem' --server-name 'gateway.example' `
  --profile signed --correlation request-1 `
  --input 'C:\AIRC\original-request.jws'
```

Use `--profile encrypted` for a compact encrypted envelope. Trust the gateway
certificate name independently of VM ID, service GUID and relay address. The
client prints only a complete correlated terminal JSON result. An `event:error`
is a completed exchange, not accepted Send. Restart the host command for another
connection. After a lost result, recover with the original signed mutation and
durable request ID; do not regenerate its identity or timestamp.

## Peer and resource boundaries

The host verifies the listener's actual bound endpoint and the accepted remote
VM ID before relaying any bytes. A VM ID restricts the partition; it is not a
native AIRC user credential. Processes inside that permitted guest still require
their native signed authority. Host/device administration and the Windows
hypervisor/kernel remain trusted for partition routing.

Returned service/address values for accepted Hyper-V sockets may differ from
the listening endpoint, as documented in Microsoft's Go socket implementation.
The adapter checks the accepted remote VM instead of treating a translated local
service GUID as an authentication failure. The guest uses peer-address metadata
to establish successful connection, without assuming that Windows returns the
literal parent alias. TLS authenticates the actual AIRC gateway. No claim relies
on undocumented AF_HYPERV support for `SO_EXCLUSIVEADDRUSE`.

Sockets use nonblocking I/O. Native handle calls and close are synchronized so
handle reuse cannot race an active syscall. Temporary would-block results retry
at bounded 50 ms intervals; read/write deadlines and close interrupt waiting
operations. Sends handle partial progress before reporting success. Socket
handles are not inherited by child processes.

One host invocation accepts one connection, with a listen backlog of one. Both
commands have a 35-second lifetime, including waiting for a peer. The host TCP
connect and guest TLS handshake each have a three-second limit. Each relay
direction forwards less than 1 MiB of ciphertext, including TLS overhead, and
uses a bounded copy buffer. Hitting the byte ceiling fails the relay. EOF
half-closes the corresponding write direction; a copy failure or cancellation
closes both streams, and the host joins both copy goroutines. The relay cannot
inspect or promise AIRC acceptance based on TLS transport completion alone.

The guest's request JSON is at most 16 KiB and its whole response at most 64 KiB.
The host has no request queue, retry, journal copy, destination negotiation or
plaintext protocol parser. A network/relay shutdown error can occur after native
acceptance; the signed request ID remains the recovery boundary.

## Deferred verification

Go source builds are available for Windows AMD64 and ARM64. No Hyper-V socket,
host registration, VM, network relay or native AIRC exchange was executed during
implementation. Compilation is not host/guest qualification.

Later acceptance needs the real provisioned host and Windows guest: exact VM
filtering and listener metadata, parent connection/implicit binding behavior,
would-block/error/partial I/O paths, TLS trust/name/ALPN refusal, signed Send and
complete bounded reads, native replay, ciphertext/connection limits, peer loss,
deadlines, half-close and joined shutdown. Verify author, target and body with
an independent AIRC observer. Existing Linux virtio-vsock evidence does not
qualify this Windows provider.

Primary references: Microsoft [Hyper-V integration sockets and registration](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/make-integration-service),
[WinSock peer addresses](https://learn.microsoft.com/en-us/windows/win32/api/winsock2/nf-winsock2-getpeername)
and the maintained [go-winio Hyper-V implementation](https://github.com/microsoft/go-winio/blob/main/hvsock.go).
