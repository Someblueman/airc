# PostgreSQL notification ingress

The selected notification API is **PostgreSQL 18 `LISTEN`/`NOTIFY`**, using
PostgreSQL protocol 3.0 over verified TLS with a client certificate. This is a
one-way, Send-only subscription. Each notification payload is one plain
[contract-v1](contract-v1.md) JSON request, the same bytes as every carrier, and
it is posted as the author it names, else as the route's bound author
([route authors](route-authors.md)).

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
      "bound_author": {"nick": "alice", "account_token_file": "/private/gateway/alice.token"},
      "guests": false,
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

The route posts a request as the author it names (a registered account with its
token always; a guest only when `guests` allows), else as its `bound_author`
(`nick`, optional `account_token_file`). A route needs a bound author or
guests; `guests` defaults to true only without a bound author. No cursor key
is needed for this source. The
database connection itself crosses verified TLS; tokens in payloads are plain
text to every listener on the channel.

## Trust boundary

The gateway trusts its own verified-TLS, client-certificate LISTEN connection
and nothing else. **Anyone who can execute `NOTIFY` or `pg_notify` on the
configured channel in that database can post as the route's bound author**, or
name any guest when guests are allowed. PostgreSQL
has no per-channel privilege: any role that can connect to the database can
notify any channel, including from functions and triggers running as that role.
Any role that can `LISTEN` there can also read every payload. Use a dedicated
database, grant `CONNECT` on it only to the intended producer roles and the
listener role, and choose a route target and bound author no wider than those
producers deserve. The database role and notifying backend PID never change the
author.

## Producer contract and loss

Publish the request JSON as the notification payload, using bound parameters
with `SELECT pg_notify($1, $2)` for the configured channel and body, for example:

```sql
SELECT pg_notify('alice_requests',
  '{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#general"},"message":"hello"}');
```

The payload is 1–7999 bytes of valid UTF-8 as received in the listener's UTF8
client encoding. PostgreSQL's normal [NOTIFY payload limit is less than 8000
bytes](https://www.postgresql.org/docs/current/sql-notify.html), measured in the
server encoding.
There is no fragmentation or result publisher in this profile. Use a mailbox
connector when a request/reply exchange or source replay is required.

Notifications become visible after the producer transaction commits. Equal
channel/payload pairs within one transaction may be coalesced. Payloads are
plain text to every listener on the channel. A successful database transaction
is not native AIRC acceptance.

Each payload must decode as a contract-v1 request whose operation is `send` and
whose target equals the route target exactly, and whose author the route
admits, before durable journal admission. Plain requests carry no
validity window. Repeating an identical body with the same `request_id`
coalesces to the original job; a different body under a used `request_id` is a
conflict and is discarded. Preserve the existing gateway journal and use stable,
unique request IDs.

LISTEN/NOTIFY is ephemeral. There is no provider checkpoint, replay queue or
transport acknowledgement. Notifications sent while disconnected, during
subscription startup or before durable native admission can be lost. Invalid,
unauthorized, expired or transiently failed admissions are discarded after one
bounded attempt. Reconnection establishes a new subscription without recovering
the gap. Requests that are malformed, refused or conflicting do not end the
subscription; a payload that is not valid UTF-8 is a protocol error and does. A closed native journal terminates the owner and service.

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

## Verification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

On 2026-10-11 (Linux AMD64, PostgreSQL 16.15) a route with a registered
`bound_author` and `guests: true` held a TLS 1.3 certificate-authenticated
LISTEN as `airc_listener`. From `psql`, an authorless `pg_notify` posted as the
bound account and one naming the guest `pg-guest` posted as that guest; an
independent `airc history` client saw both. Adapter tests cover guest and
bound-author routes, a guest refused on a bound route, an anonymous request,
target and operation refusals, capacity and a closed journal.

Before the author model, on 2026-10-10, adapter unit tests ran plain requests through a real journal:
admission as the route principal with idempotent replay, and refusal before
intake of a mismatched target, non-send operation, malformed JSON, unknown or
revoked principal, binding mismatch and missing capacity. A closed journal ends
the owner. Wire tests accept UTF-8 JSON payloads with whitespace and refuse
invalid UTF-8, oversized payloads and channel mismatch.

The same day, on Linux AMD64 with PostgreSQL 16.15 (not the selected 18), a
gateway with no signing, recipient or response keys held a TLS 1.3
certificate-authenticated LISTEN as `airc_listener`. Plain `pg_notify` calls
from `psql` delivered two messages, including non-ASCII text, to a scratch
`aircd`. An independent `airc history` client saw them from the binding's
account in the routed room, and the journal attributed both to the route
principal. A wrong-target request and non-JSON text were discarded without
intake on the same backend connection. PostgreSQL 18, remote databases,
pre-admission loss, malformed backend frames and shutdown under load were not
exercised against a real server.
