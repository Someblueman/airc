# Apache CouchDB change-feed ingress

The selected change-feed API is **Apache CouchDB 3 `/{db}/_changes`**, over HTTPS
with an explicit Basic-auth account. This is a read-only, one-way subscription:
immutable request documents carry encrypted author-signed AIRC Send intents.
It has no database writer, reply publisher or native read operations. Use a
mailbox connector when a request/reply exchange is required. Testing and
deployment are deferred.

## Configuration

Add a top-level `couchdb_changes` array with at most 16 owners:

```json
{
  "couchdb_changes": [{
    "id": "alice-changes",
    "enabled": true,
    "endpoint": "https://couchdb.example.net",
    "database": "airc_alice_requests",
    "namespace": "02093010-169b-4a88-af99-a29242189234",
    "username": "airc_reader",
    "password_file": "/private/gateway/couchdb-password",
    "initial_sequence": "0",
    "route": {
      "principal": "alice",
      "target": {"kind": "room", "name": "#general"}
    }
  }]
}
```

Replace resource and identity placeholders and generate a fresh namespace UUID
for the provisioned feed. The endpoint is a canonical HTTPS origin with a
lowercase DNS hostname and optional non-default numeric port; omit port 443.
Paths, queries, credentials
and fragments are refused. The database name is 1–64 ASCII characters, beginning
with a lowercase letter and then using lowercase letters, digits or `_$()+-`.
Only one owner may select an endpoint/database pair in the configuration.

The username is 1–64 ASCII letters, digits, periods, underscores or hyphens.
The password file contains exactly 32–256 printable non-space ASCII bytes,
without a newline, and must be private. Optional `tls_ca_file` selects an
explicit PEM trust pool; otherwise system certificate trust applies. TLS checks
the endpoint hostname. No proxy, redirect, cookie session or ambient credential
lookup is used. Restart after rotating credentials. CouchDB's [Basic
authentication](https://docs.couchdb.org/en/stable/api/server/authn.html#basic-authentication)
does not establish the AIRC sender.

The fixed principal must be active, hold a signing key and grant Send to the
exact target. Configure gateway request-decryption keys. No response signer or
cursor signing key is needed for this Send-only profile. Provision the database,
read account, producer access and immutability policy separately; the gateway
does not create resources or inspect the provider's access policy.

## Immutable request documents

Use a dedicated, non-partitioned request database. Each request is created once,
with these exact application fields:

```json
{
  "_id": "airc-<lowercase SHA-256 hex of the exact compact JWE>",
  "type": "airc_request_v1",
  "namespace": "02093010-169b-4a88-af99-a29242189234",
  "airc_jwe": "<whole compact JWE, at most 16384 ASCII bytes>"
}
```

CouchDB adds `_rev`. The gateway accepts only a first-generation revision
`1-<32 lowercase hex digits>` with one leaf and matching change/document IDs and
revision. Attachments, conflicts, deleted requests, extra application fields,
duplicate JSON keys and malformed envelopes are refused. Provision producers
to create immutable documents and retain them; do not update, delete, purge or
reuse request IDs while the feed is active. Live `_design/` documents are
bounded administrative control records and do not submit AIRC messages.

CouchDB [guarantees only the latest change for each
document](https://docs.couchdb.org/en/stable/api/database/changes.html). The
immutable-document rule is therefore essential: this connector does not
reconstruct intermediate versions of mutable database rows. The feed can also
repeat records and does not provide a global message-order guarantee.

The gateway requests normal unfiltered ascending pages with documents, all leaf
revisions and conflict information, a limit of 16 and no attachment bodies.
It validates the complete bounded page before dispatching any request. Every
request must match the configured namespace and exact signed native principal,
binding, target and Send operation. Database IDs, revisions and sequence numbers
grant neither native authority nor freshness.

## Durable progress and recovery

An omitted `initial_sequence` means `"0"`. A nonzero value is an explicit choice
to start after a captured provider sequence; it is used only for a new namespace.
The special live-only `now` position is refused. This profile supports canonical
nonnegative integer sequences and CouchDB 3 cluster sequence strings beginning
with a digit and containing only ASCII letters, digits, hyphens and underscores.
Both are represented as strings; arbitrary JSON-object sequences and other
opaque formats are outside this selected profile.

The gateway stores only a namespace, scope fingerprint, revision and sequence in
its existing journal. A page's `last_seq` is committed with compare-and-swap only
after every request in that page has a durable native Send result. Control
records require no native admission. Empty pages may advance the provider's
sequence. No individual row sequence is used to skip the rest of a page.

A crash before checkpoint publication repeats the page. Native request IDs,
original signed validity and existing journal deduplication remain authoritative.
An expired encrypted Send can recover only an identical retained result under
current durable grants. It cannot create a new job or renew freshness. Preserve
the gateway journal across restart; a stored progress position is not a delivery
receipt, and provider document creation is not AIRC admission.

A malformed, conflicted, deleted, unauthorized or expired unretained request
leaves the page checkpoint unchanged. The owner retries with bounded backoff;
it does not silently skip the request. Operators must reconcile pending native
work before deliberately skipping history or replacing the source. Such a
migration requires a stopped owner, a fresh namespace, an explicitly selected
starting sequence and matching namespace on subsequent requests. Never reuse a
namespace for a restored, purged or replacement database.

A native request-identity conflict or a closed/conflicting journal checkpoint
stops the owner and service for recovery. Other source/admission errors keep the
last complete page position and retry.

The scope fingerprint is SHA-256 over the following values joined with NUL,
without a trailing separator:

```text
airc-couchdb-changes-v1, realm, namespace, owner-id, endpoint, database,
principal, binding, target-kind, target-name, initial-sequence
```

`CouchDBChangesScope` constructs it. Reusing a namespace with a changed scope is
refused, including a changed initial sequence. Credentials and trust-file paths
are excluded so they can rotate. Progress ownership is permanent and bounded
to 128 namespaces; there is no automatic cleanup or reset. Journal schema 21
adds this table without changing existing journal rows. Older gateway binaries
refuse the newer schema; retain a pre-upgrade backup if rollback is required.

## Bounds and deferred verification

Each owner has a serial 60-second pass budget, including a 10-second HTTP timeout
and at most 16 three-second native admissions. Shared request capacity covers
the fetched page through its durable checkpoint. Responses are limited to
512 KiB, JSON nesting to 16 levels and sequence tokens to 2048 bytes. Each client
has at most two connections and 8 KiB response headers. Idle polling waits one
second; error backoff is capped at 15 seconds. Shutdown cancels and joins owners
before clearing password material and closing idle connections.

Readiness confirms local credential/trust preparation, progress provisioning
and worker launch; it does not confirm provider access or a completed feed page.

Production `go build ./...` passes for Darwin ARM64 and Linux AMD64/ARM64 with
`CGO_ENABLED=0`. Source review, formatting, whitespace and source-size advisory
checks pass. These checks do not establish runtime acceptance.

Real CouchDB interoperability and access policy, durable checkpoint migration,
source loss/poison recovery, original native author/target/body, replay, size
limits, cancellation and joined shutdown remain unexecuted by owner direction.
