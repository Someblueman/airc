# GitHub inbound webhooks

The chosen inbound provider is **GitHub.com**, using its HTTPS webhook API.
The `github_webhook` listener imports newly created issue/PR comments as
bounded AIRC sends. GitHub's body signature proves provider provenance;
numeric repository and author grants select the existing AIRC principal and
target. This is a one-way event importer with an admission acknowledgement.
Implementation is available; testing and deployment are deferred.

## Selected webhook scope

GitHub.com inbound issue-comment webhooks are the selected webhook connector
for this roadmap. Outbound event callbacks are explicitly excluded: the
catalogue describes them as optional, and the required outcome is authenticated
message ingestion under an existing AIRC identity. This one-way provider
profile supplies that outcome with a durable admission acknowledgement;
existing gateway receipt and read operations provide their own recovery paths.
There is no outbound callback destination, subscription, delivery queue or
callback worker in this profile, and no outbound implementation is claimed.

## Configuration and provider setup

Add a listener to the ordinary gateway configuration:

```json
{
  "id": "github-comments",
  "protocol": "github_webhook",
  "address": "0.0.0.0:8443",
  "enabled": true,
  "tls_cert_file": "/absolute/private/server-cert.pem",
  "tls_key_file": "/absolute/private/server-key.pem",
  "operations": ["send"],
  "github_webhook": {
    "secret_file": "/absolute/private/github-secret",
    "freshness_seconds": 300,
    "routes": [
      {
        "repository_id": 123456,
        "author_id": 789012,
        "principal": "github-author",
        "target": {"kind": "room", "name": "#engineering"}
      }
    ]
  }
}
```

Replace the example IDs with the actual GitHub repository and user IDs.
The principal must already exist, be active, and have `send` permission for
the exact target through its existing binding. Each repository/author pair
has one route; configure 1–128 routes. Usernames and repository names are not
authority, so renames do not silently change grants. The secret file is an
owner-private regular file containing 32–1024 printable ASCII bytes, with no
trailing newline or trimming. Use a distinct randomly generated secret for
this provider integration and configure the identical value in GitHub.

In GitHub, configure `https://YOUR-AUTHORIZED-ENDPOINT:8443/v1/github/events`,
JSON content, the provisioned secret, and issue-comment events. Keep SSL
verification enabled and use a certificate trusted by GitHub. TLS is required
even for local configured listeners. The service does not create the webhook,
obtain certificates, provision identities, or post anything back to GitHub.
GitHub's signed setup `ping` receives HTTP 204 without an AIRC operation.
See [GitHub's event contract](https://docs.github.com/en/webhooks/webhook-events-and-payloads#issue_comment)
and [signature setup](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries).

## Authentication, freshness and replay

The exact request body must match a single `X-Hub-Signature-256` value with
the `sha256=` prefix. Verification uses HMAC-SHA256 and a constant-time
comparison before JSON interpretation. Duplicate signature/event headers,
compressed bodies, duplicate JSON keys, malformed Unicode, case aliases of
selected fields, and missing/null required fields are refused. Additional
GitHub fields are allowed within the overall size/depth bounds.

Only `issue_comment` with signed action `created` can create a send. Comment,
repository, issue and sender IDs must be positive integers; the comment's
author ID must equal the sender ID and match a configured grant. The selected
profile requires equal original `created_at` and `updated_at` values in UTC.
Edits, deletions, unmapped senders and other events cannot create AIRC jobs.
The original creation time must be nonfuture and within the configured
1–300 second freshness window. Receiving or retrying an event never renews
that window. GitHub's event and delivery headers are not signed body fields;
neither supplies identity, freshness or a durable request ID.

The durable request ID derives from the GitHub.com repository/comment ID.
The journal binds it atomically to the exact author, issue, timestamp, body,
principal, binding and target. Matching redeliveries return the existing job;
changed content, mapping or expiry conflicts instead of creating another
send. Markers remain until the original freshness window has ended and the
native job is gone. The event table is capped at 16,384 rows and shares the
existing journal-byte limit. Migration 19 adds this table without changing
existing jobs or replay tables. Current durable revocation checks still apply.

The comment body must be nonempty, valid UTF-8, at most 4096 bytes, and usable
unchanged under AIRC's message rules. Oversized or normalization-changing
messages are refused whole. AIRC delivery uses the existing durable worker,
receipt and uncertainty handling. HTTP 202 means durable admission and
returns the current gateway result; it does not assert upstream acceptance.

GitHub does not automatically retry failed deliveries. Manual redelivery
must still satisfy original freshness; stale comments are refused. See
[GitHub's failed-delivery guidance](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries).

## Bounds and deferred verification

Production `go build ./...` passes on Darwin ARM64 and Linux AMD64/ARM64
(Linux cross-builds use `CGO_ENABLED=0`). Formatting, whitespace and advisory
source-size checks pass. Review covered provider authentication, identity
grants, atomic replay admission, freshness deadlines and handler ownership.
Existing migration fixtures were updated for schema 19 but were not run.

Payloads are limited to 256 KiB and 16 JSON nesting levels. Shared connection
and request limits bound intake; headers have a two-second read limit,
requests and writes five seconds, admission three seconds, and idle
connections 30 seconds. HTTPS handlers are joined before the HMAC secret is
cleared. No history API or outbound callback is exposed by this importer.

Real GitHub delivery/ping, TLS trust, exact native AIRC author/target/body,
freshness/refusal boundaries, durable restart/redelivery, migration, capacity
and shutdown need verification. No tests, live webhooks, schema probes,
normal/race acceptance or deployment were run for this implementation.
