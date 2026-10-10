# Amazon SQS managed queue

The concrete managed-queue choice is **Amazon SQS Standard queues**, using the
AWS JSON API over verified HTTPS and Signature Version 4. The gateway consumes
an explicit request queue and publishes signed, encrypted results to a distinct
configured queue. This implementation has been reviewed and compiled; runtime,
security, AWS interoperability and native AIRC acceptance are deferred in
[ROADMAP.md](ROADMAP.md).

## Configuration and provisioning

An optional top-level `sqs` array contains at most 16 owners. Each owner binds
one request queue to one existing signing principal, target, operation set and
response recipient. Example values below are placeholders to replace with actual
provisioned resources:

```json
{
  "sqs": [{
    "id": "requests",
    "enabled": true,
    "region": "eu-west-2",
    "request_queue_url": "https://sqs.eu-west-2.amazonaws.com/123456789012/airc-requests",
    "credential_file": "/private/gateway/aws-sqs.json",
    "route": {
      "principal": "alice",
      "target": {"kind": "room", "name": "#general"},
      "operations": ["send", "history.read", "search", "context.read"],
      "result_queue_url": "https://sqs.eu-west-2.amazonaws.com/123456789012/airc-results",
      "recipient_key_id": "alice-results",
      "recipient_public_key_file": "/private/gateway/alice-results.pem"
    }
  }]
}
```

The principal must already grant the exact canonical target and every route
operation. Read-only routes are valid. History/search require the existing cursor
key. Configure the gateway's request-decryption `recipient_keys`, its Ed25519
`response_signing_key`, and the route's PKIX P-256 response public key. Credential
and key paths participate in the existing regular-file, private-permission and
no-alias checks.

The private credential file is a bounded JSON object with `access_key_id`,
`secret_access_key` and optional `session_token` strings. There is no ambient
environment, profile, metadata-service or credential-refresh chain. Rotate the
file and restart the gateway before temporary credentials expire. Provider
credentials authenticate AWS API calls; they do not identify an AIRC sender.

Only canonical `https://sqs.<region>.amazonaws.com/<12-digit-account>/<name>`
URLs are accepted. Request and result queues must be Standard queues in the
same region/account, with names of 1–80 ASCII letters, digits, underscores or
hyphens. Their roles cannot overlap another SQS owner. Custom endpoints, FIFO,
China/GovCloud endpoints, ports, user information, escaping, query strings and
fragments are outside this profile. TLS uses system roots and verified hostname;
proxies, redirects and compressed responses are disabled.

Provision the queues, IAM policy and a separate Standard dead-letter queue before
startup. The gateway never creates or changes cloud resources. It checks both
queue ARNs, Standard type and message-size limits before intake, and requires a
valid source `RedrivePolicy` with a distinct same-region/account Standard DLQ ARN
and `maxReceiveCount` of 1–1000. Request capacity must be at least 16 KiB; result
capacity at least 64 KiB. The configured DLQ policy is checked, not the DLQ's
existence, permissions or operational redrive behavior. The deployment must
ensure the DLQ allows this source, and must monitor refused messages there.
[AWS documents the redrive policy and queue attributes](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_GetQueueAttributes.html).

Grant the gateway `sqs:GetQueueAttributes` on both queues, `sqs:ReceiveMessage`
and `sqs:DeleteMessage` on requests, and `sqs:SendMessage` on results. Provision any
required KMS permissions separately. Restrict producer/consumer IAM permissions
to their intended queues. A service `ready` record means local preparation and
owner startup; it does not assert successful AWS authentication or queue checks.

## Request, result and replay contract

Submit the existing compact JWE containing a signed AIRC request as the exact SQS
message body: no JSON wrapper, attributes or newline. The body is at most 16 KiB.
Its signature and configured signing key establish the principal and binding;
the signed realm, request ID, target, operation and original validity establish
intent. SQS MessageId, sender attributes, timestamps,
receipt handles and redelivery count grant no author, freshness or replay rights.
Only `send`, `history.read`, `search` and `context.read` are dispatched. Reads
return a complete bounded result with the existing page/cursor contract; there
are no streaming watches.

The result queue receives one compact protected response token using the existing
Ed25519 JWS inside P-256 ECDH-ES/A256GCM JWE profile. The payload is `SQSResult`:
`version:1`, `protocol:"aws/sqs"`, signed `request_id`, both fixed queue URLs,
target, and exactly one `result` or `error`. `result` is the existing whole
`DetachedResult`. Response scope includes realm, principal and binding; its route
is lowercase SHA-256 hex of these exact bytes, with no trailing separator:

```text
sqs-response NUL request-queue-url NUL result-queue-url NUL target-kind NUL target-name
```

`adapter.SQSResponseScope` constructs it. Consumers verify the response signing
key, recipient, expected scope, queue pair, request ID and response lifetime.
Results retain the 32 KiB clear payload and 64 KiB token limits, with a 120-second
validity. SendMessage explicitly disables queue delay; clients must consume
promptly. A whole result that exceeds capacity produces a protected capacity
error, and the input remains unacknowledged. Nothing is silently truncated.

Successful durable Send admission or a complete read is published before deleting
the request with the receipt handle from that exact receive. A transport failure
or an error result leaves the input unacknowledged. Fresh duplicate Sends use
the existing durable journal identity and conflict checks. An expired encrypted
Send can recover only an identical retained journal result under current grants;
it cannot create, reschedule or extend intent. Unadmitted expired requests and
expired reads remain refused for redrive. Requests with purged journal results
cannot use this lookup-only recovery. Submit fresh read requests for new results.

Standard queues can redeliver even after a successful deletion; consumers dedupe
results by authenticated request identity. Publication and deletion confirm queue
operations, not native AIRC acceptance. Inspect the returned durable Send state.
These semantics follow AWS's [at-least-once delivery](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/standard-queues-at-least-once-delivery.html)
and [receipt-handle deletion contract](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteMessage.html).

## Bounds and ownership

Each owner polls serially for one message with a 10-second long poll and explicit
30-second visibility timeout. Receive calls have a 15-second context deadline;
processing, publication and deletion together have 10 seconds, with 3 seconds
for authentication/dispatch. Idle polling uses the separately bounded owner
connections, while message authentication through result publication shares the
service request limit. Failed API calls back off to at most 10 seconds; every
iteration yields at least 250 ms. Refused messages are not deleted. Both receive
and send verify AWS's body MD5; cryptographic author trust comes from the signed
application envelope, not MD5.

AWS responses have a 256 KiB body bound, depth and duplicate-field checks, and
8 KiB HTTP header bound. Requests use exact-body SigV4 signing, including the
temporary session token when configured. See the [AWS JSON protocol](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-making-api-requests-json.html)
and [SigV4 canonical request](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html).
Cancellation interrupts polling and in-flight requests, joins each consumer,
then closes idle connections and clears owned credential bytes. No new durable
schema or dependency is introduced by this connector.
