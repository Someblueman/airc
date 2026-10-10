# PostgreSQL notification ingress

The selected notification API is **PostgreSQL 18 `LISTEN`/`NOTIFY`**, using
PostgreSQL protocol 3.0 over verified TLS with a client certificate. This is a
one-way, Send-only subscription. Each notification carries a complete encrypted,
author-signed native AIRC Send intent. Testing and deployment are deferred.

## Configuration

Add a top-level `postgres_notify` array with at most 16 owners:

```json
{
  "postgres_notify": [{
    "id": "alice-notifications",
    "enabled": true,
    "address": "192.0.2.10:5432",
    "tls_server_name": "postgres.example.net",
    "tls_ca_file": "/private/gateway/postgres-ca.pem",
    "client_cert_file": "/private/gateway/postgres-client.pem",
    "client_key_file": "/private/gateway/postgres-client-key.pem",
    "database": "airc_requests",
    "username": "airc_listener",
    "channel": "alice_requests",
    "route": {
      "principal": "alice",
      "target": {"kind": "room", "name": "#general"}
    }
  }]
}
```

Replace the documentation address, paths and identities with provisioned values.
The address is a canonical numeric IP and port; unspecified, multicast, mapped
IPv6 and scoped addresses are refused. `tls_server_name` is the canonical
lowercase DNS name or IP checked against the server certificate. Optional
`tls_ca_file` selects a PEM trust pool; omission uses system roots. TLS 1.2 or
newer is required. There is no plaintext or password fallback.

Database, username and channel use `[a-z_][a-z0-9_]{0,62}`. Each
address/database/channel tuple must be unique. The selected profile requires an
Ed25519 client private key in a private file and a currently valid, non-CA leaf
certificate with explicit ClientAuth usage, digital-signature key usage and
Common Name exactly equal to the database username. Certificate and trust files
use the existing bounded public-file checks. Restart to rotate credentials.

Provision the database, role, server trust and a matching `hostssl` certificate
authentication rule outside the gateway. PostgreSQL's [certificate
authentication](https://www.postgresql.org/docs/current/auth-cert.html) verifies
the client certificate and matches its identity to the database role. This
profile uses exact Common Name matching, without a username map. The gateway
requires the server to request the configured client certificate and then send
AuthenticationOk; it cannot inspect the server's authentication policy.

The fixed AIRC principal must be active, have a signing key and grant Send to the
exact target. Configure gateway request-decryption keys. No response signer or
cursor key is needed. Database authentication, channel membership and the
notifying backend PID never establish an AIRC author.

## Producer contract and loss

Publish the whole compact JWE as the notification payload, using bound
parameters with `SELECT pg_notify($1, $2)` for the configured channel and token.
The payload is 1–7999 ASCII bytes. PostgreSQL's normal [NOTIFY payload limit is
less than 8000 bytes](https://www.postgresql.org/docs/current/sql-notify.html).
There is no fragmentation or result publisher in this profile. Use a mailbox
connector when a request/reply exchange or source replay is required.

Notifications become visible after the producer transaction commits. Equal
channel/payload pairs within one transaction may be coalesced. Notifications
are visible to database users, so the native intent must remain encrypted as
well as signed. A successful database transaction is not native AIRC acceptance.

Every payload is checked against its original native validity period and exact
signed principal, binding, target and Send operation. It is admitted only with a
durable result matching the native request ID. An expired encrypted Send can
recover only an identical retained result under current durable grants; it
cannot renew freshness or create another job. Preserve the existing gateway
journal and stable request IDs across restart.

LISTEN/NOTIFY is ephemeral. There is no provider checkpoint, replay queue or
transport acknowledgement. Notifications sent while disconnected, during
subscription startup or before durable native admission can be lost. Invalid,
unauthorized, expired or transiently failed admissions are discarded after one
bounded attempt. Reconnection establishes a new subscription without recovering
the gap. A closed native journal terminates the owner and service.

## Protocol and resource bounds

Each owner holds one shared connection slot for its entire session. Startup is
bounded to ten seconds and 64 backend frames, with at most 16 notifications
queued before LISTEN completes. The client sends a PostgreSQL SSLRequest before
TLS and refuses a denied upgrade. It then sends a protocol 3.0 startup requesting
UTF8, requires certificate authentication completion and issues only the fixed
quoted LISTEN command. Password authentication mechanisms are refused.

Backend frames are limited to 16 KiB. An idle subscription may wait indefinitely
for its first frame byte, with context cancellation closing the connection. A
partially received frame has a five-second deadline; failure discards the
session. TCP keepalive uses 30 seconds. Bounded notices and parameter messages
are accepted, with at most 32 between notifications. Malformed protocol messages
end the session. There is no per-notification SQL query.

Each native admission uses shared request capacity and a three-second budget,
including capacity wait. Reconnection backs off from one to at most 15 seconds.
Shutdown cancels and joins sessions before clearing retained private-key bytes.
No dependency or gateway journal migration is added.

Readiness confirms local certificate and authority preparation and worker
launch. It does not confirm database access or a successful LISTEN subscription.

Production `go build ./...` passes for Darwin ARM64 and Linux AMD64/ARM64 with
`CGO_ENABLED=0`, after removing an unused adapter import caught by compilation.
Source review, formatting, whitespace and source-size advisory checks pass.
These checks do not establish runtime acceptance.

Real PostgreSQL certificate interoperability, ordinary producer/native AIRC
delivery, pre-admission loss, replay, malformed frames, capacity, cancellation
and joined shutdown remain unexecuted by owner direction.
