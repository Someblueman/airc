# Configured HTTP service lifecycle

`airc-gateway serve --file /absolute/path/to/config.json` runs the partial HTTP
service and durable workers. It uses the same strict config parser as validation.
Only explicitly enabled `protocol: "http"` instances can start, and their allowlist
can currently contain send, reply and receipt.get. Richer operations and other
protocols still fail enabled validation; they remain required pending work.
No account is provisioned and no upstream dial occurs without a journaled grant.

Before binding, startup validates all credential snapshots and owner-verified
TLS certificate/key pairs. Files are bounded to 1 MiB; private keys require an
owner-only parent directory and file, without symlinks. Certificates must parse,
match their key and be currently valid. These checks do not install trust roots
or replace client hostname/trust verification. Native non-Unix credential support
remains unavailable. The journal's exclusive owner lock precedes listener startup.

Plain HTTP binds loopback only; non-loopback addresses require provisioned TLS.
TLS listeners negotiate HTTP/1.1 or HTTP/2 with a TLS 1.3 minimum. All enabled
listeners must bind successfully before a single JSON ready record is emitted.
The record gives configured listener IDs and their actual bound addresses,
including allocated port-zero addresses. It establishes bound ingress and worker
startup, not upstream reachability or guaranteed delivery.

Ingress connections share a process-wide pool capped at configured `sessions`.
This ingress pool is separate from the backend's pool, also capped at `sessions`.
Excess accepted sockets close immediately. HTTP request-slot limits and protocol
header/body/idle deadlines apply within those connection slots. These bounds do
not establish a throughput SLA or complete authentication-flood fairness evidence.

SIGINT/SIGTERM stops admission, cancels service-owned attempts and handler contexts,
closes listeners, gives HTTP shutdown five seconds before forced socket closure,
and joins tracked handlers and delivery workers. Delivery workers retain their
independent five-second outcome-saving bound. The journal stays open until those
owners have finished, and its close error is reported. Pending jobs remain available
for reopening. Startup failure closes any earlier bound listener and journal lock;
invalid TLS is rejected before journal creation. Nothing is installed globally.

## Isolated use and evidence

Provision a private state directory and gateway bearer file with the existing
`credential init` command. Configure an already provisioned account on an owned
isolated AIRC fixture using explicit addresses/token-file references; do not use
an installed daemon's defaults. Provision TLS paths in private directories, then:

```sh
go run ./cmd/airc-gateway config validate --file /absolute/path/to/fixture-config.json
go run ./cmd/airc-gateway capabilities --file /absolute/path/to/fixture-config.json
go run ./cmd/airc-gateway serve --file /absolute/path/to/fixture-config.json
```

The local capabilities command describes compiled intake and enabled/disabled
configured instances without starting anything. It is not negotiated readiness.
Remote capability discovery remains authenticated and scoped to the caller.

The native acceptance test builds an owned race-enabled CLI binary, starts an
owned immutable daemon and a gateway with a clean temporary configuration/state,
uses a real trusted TLS HTTP/2 client to submit and poll, and verifies the post
with an independent AIRC observer. SIGINT exits successfully; the listener cannot
be dialed afterward and reopening the journal confirms one saved receipt/send.
Every process is PID-owned and cleaned up. Separate tests verify multi-listener
bind rollback, invalid-key pre-journal rejection, no-follow/private-key/size checks,
and enabled-config operation/remote-plaintext rejection.

```sh
go test -race -count=1 ./integration/gateway -run TestGatewayServeCLI
go test -race ./internal/gateway/service ./internal/gateway/auth ./internal/gateway/config ./cmd/airc-gateway
```

Full outage/restart process acceptance, slow-client/load/resource measurements,
health/readiness, telemetry, browser origins, richer HTTP operations, operator
resolution/backup and the rest of the complete catalogue remain pending. Native
AIRC backend TLS listener acceptance remains distinct from this verified HTTPS
ingress. This service is partial and is not deployed against live AIRC.
