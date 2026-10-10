# Amazon EventBridge through SQS

The selected cloud event bus is **Amazon EventBridge Classic**, using a custom
event bus rule with an Amazon SQS Standard queue target. An explicit
`event_bridge` profile on an existing SQS owner accepts the complete matched
event and extracts its encrypted, author-signed AIRC intent. The SQS owner
provides serial delivery, bounded reads, protected results and joined shutdown.
Implementation is available; testing and cloud deployment are deferred.

## Configuration and cloud resources

Configure an SQS owner as described in [sqs.md](sqs.md), adding:

```json
"event_bridge": {
  "source": "com.example.airc",
  "detail_type": "AIRC Request"
}
```

The owner still has a fixed AWS region, request/result queue pair, signing
principal, target, operation allowlist and response recipient. Source uses
1–128 ASCII letters, digits, periods, underscores or hyphens and cannot start
with `aws.`. Detail type uses 1–128 printable ASCII bytes without leading or
trailing whitespace. Both compare exactly. Omitting `event_bridge` preserves
the existing raw-JWE SQS contract; the two input formats are never autodetected.

Provision a Classic custom event bus, a rule selecting the configured source
and detail type, and the request queue as its target. Select **Matched events**
so the whole event reaches SQS. Do not use Input, InputPath or an input
transformer. Use the same AWS account and region as the configured queue. See
[AWS's rule target input options](https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-create-rule-wizard.html).

The request queue needs at least 32 KiB message capacity and the existing
same-account/region Standard DLQ redrive policy. The result queue still needs
64 KiB. Restrict the queue's resource policy to the intended rule, for example
an `events.amazonaws.com` principal with `sqs:SendMessage` on the exact queue
ARN and an `ArnEquals` condition on `aws:SourceArn` naming the exact rule ARN.
Grant producers `events:PutEvents` on the intended bus. Restrict all alternate
queue writers and consumers through deployment IAM. See [AWS's SQS target
permissions](https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-use-resource-based.html#eb-sqs-permissions).

Gateway credentials retain the existing SQS receive/delete/result-publish and
attribute-read permissions. No EventBridge control-plane permission is needed.
The gateway checks queue attributes and DLQ configuration, but does not create,
inspect or alter the event bus, rule, producer IAM or queue access policy.
The envelope has no authenticated bus/rule identifier. Its metadata provides
filters and correlation; only the enclosed signed intent grants AIRC authority.
Cloud resources, their policies and actual credentials remain deployment inputs.

## Event and native intent

Submit a custom event with the configured Source and DetailType, and a Detail
JSON object containing exactly one `airc_jwe` string. EventBridge delivers an
envelope with this shape (the token below is a placeholder):

```json
{
  "version": "0",
  "id": "12345678-1234-1234-1234-123456789abc",
  "detail-type": "AIRC Request",
  "source": "com.example.airc",
  "account": "123456789012",
  "time": "2026-10-10T12:00:00Z",
  "region": "eu-west-2",
  "resources": [],
  "detail": {"airc_jwe": "COMPACT-ENCRYPTED-SIGNED-AIRC-REQUEST"}
}
```

The whole envelope is at most 32 KiB, with at most eight JSON nesting levels.
Version must be `0`, ID a canonical lowercase UUID, and account/region equal
the configured request queue. Time must parse as RFC 3339. The resources array
is required, with at most 32 nonempty printable ASCII values of up to 512 bytes
each. An optional `replay-name` is limited to 64 printable ASCII bytes. Unknown,
duplicate, case-aliased and null fields are refused. This is a bounded custom
event profile; arbitrary AWS service event schemas are not interpreted. See
[the AWS event metadata contract](https://docs.aws.amazon.com/eventbridge/latest/ref/events-structure.html).

The inner compact JWE is at most 16 KiB and cannot contain whitespace or
application fragments. Existing signed intent validation establishes the
realm, principal, binding, exact target, operation, request ID and validity.
Only configured `send`, `history.read`, `search` and `context.read` operations
are available. Event ID, event time, source, account, replay name and SQS
metadata cannot supply native authority, renew validity or replace the native
request ID. An archive replay still needs fresh valid intent or an exact
retained Send result; it cannot make an expired read or unadmitted Send execute.

## Protected results and recovery

Results use the existing SQS result queue and signed/encrypted response format,
with `protocol:"aws/eventbridge/sqs"`. The payload also includes `event_bridge`
metadata containing `id`, `source`, `detail_type`, `account` and `region` from
the accepted outer event. The protected native `request_id` remains the durable
identity. Consumers verify both that identity and the expected event correlation.

`SQSResponseScope` uses a separate route digest for this mode. Its lowercase
SHA-256 hex input is the following sequence, without a trailing separator:

```text
eventbridge-sqs-response NUL request-queue-url NUL result-queue-url NUL target-kind NUL target-name NUL source NUL detail-type
```

Realm, principal and binding remain separate scope fields. Ordinary SQS scopes
and payloads are unchanged. Complete protected results must be published before
the exact received request is deleted. Authentication refusals, transient errors
and whole-result capacity failures leave the input for redelivery/redrive.
Duplicated cloud delivery or uncertain publication may produce duplicate results;
clients deduplicate using authenticated native request identity. Publication is
queue acceptance, not proof of successful native delivery.

Existing 32 KiB clear-result, 64 KiB encrypted-result and 120-second response
lifetime limits apply. SQS API responses remain bounded to 256 KiB. Shared
request capacity covers envelope parsing, native dispatch and result publication;
idle polling uses the bounded SQS owner count. Credential and response-signing
buffers are cleared after owners join. No new dependency or durable schema is
introduced.

## Deferred verification

Production `go build ./...` passes for Darwin ARM64 and Linux AMD64/ARM64 with
`CGO_ENABLED=0`. Source review, formatting, whitespace and source-size advisory
checks pass. These checks do not establish runtime acceptance.

Real PutEvents/rule/queue delivery, IAM restrictions, exact native author/target/
body, protected whole reads, input refusals, original expiry, retained replay,
publish-before-delete, restart, capacity and joined shutdown remain untested.
No AWS resource or native AIRC service was contacted during implementation.
