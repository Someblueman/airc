# Postmark inbound email

The chosen email ingestion service is **Postmark**, using its inbound JSON
webhook over HTTPS. The `postmark_email` listener imports an encrypted,
author-signed AIRC Send intent from `TextBody`. This is a one-way importer;
it sends no email and exposes no native reads. Testing and deployment are
deferred.

## Configuration

Add a listener to an existing gateway configuration:

```json
{
  "id": "email-ingress",
  "protocol": "postmark_email",
  "address": "0.0.0.0:8443",
  "enabled": true,
  "tls_cert_file": "/absolute/private/server-cert.pem",
  "tls_key_file": "/absolute/private/server-key.pem",
  "operations": ["send"],
  "postmark_email": {
    "username": "airc-inbound",
    "password_file": "/absolute/private/postmark-webhook-password",
    "webhook_url": "https://mail.example.com/v1/postmark/email",
    "routes": [
      {
        "from": "author@example.com",
        "to": "provisioned-address@inbound.postmarkapp.com",
        "principal": "email-author",
        "target": {"kind": "room", "name": "#engineering"}
      }
    ]
  }
}
```

Replace the addresses and endpoint with provisioned values. Each exact From /
OriginalRecipient pair selects one active signing principal, its existing
binding and an explicitly granted Send target. Configure 1–128 unique routes.
The existing gateway P-256 decryption key and author signing public key are
required. No response signer is needed for this send-only listener.

The username uses 1–64 ASCII letters, digits, hyphens or underscores. The
password file must be an owner-private regular file containing 32–128 bytes
using the same alphabet, without a newline. Use a separately generated random
password for this webhook. Mailbox addresses use an ASCII dot-atom local part
from letters, digits, `.`, `_`, `%`, `+`, `-`, and a lowercase DNS domain.
Local-part case is significant; display names, address normalization and
wildcard routes are not used.

Configure the Postmark inbound stream's webhook URL with these HTTP Basic
credentials, using Postmark's documented userinfo form. Keep credentials out
of the gateway `webhook_url` and logs. The gateway URL is exactly
`https://<lowercase-dns-name>/v1/postmark/email`, without an explicit port,
query, fragment or escaped path. The public default HTTPS port may forward
to a private TLS listener, preserving the exact Host and path. The gateway
requires TLS on its own connection and ignores forwarded headers for routing.
Provision the trusted certificate, inbound stream/address and any forwarding
domain separately. See [Postmark's endpoint authentication](https://postmarkapp.com/developer/webhooks/webhooks-overview)
and [inbound JSON contract](https://postmarkapp.com/developer/webhooks/inbound-webhook).

## Trust and admission

Postmark supports HTTP Basic authentication for webhooks; it does not sign the
body. The listener requires one canonical Basic Authorization value and uses
a fixed-size constant-time credential comparison. HTTPS protects the request
in transit. Any TLS-terminating forwarding component is part of this trust
boundary. Startup clears the loaded password and temporary header buffers,
retaining only the expected header's SHA-256 digest and length. The service
clears that digest after handlers have joined during shutdown.

The JSON must identify the `inbound` stream. `FromFull.Email` must equal the
bare `From`; `ToFull` must contain exactly one address whose Email equals
`OriginalRecipient`. All must match the configured route exactly. Cc/Bcc
recipients and attachments are refused. `Attachments` must be an empty array.
An optional `RecordType` must be `Inbound`. The provider MessageID must have
canonical lowercase UUID form, but is metadata rather than native request
identity. Additional bounded fields are accepted; duplicate keys, selected
field case aliases, malformed Unicode and missing/null required values fail.

`TextBody` is limited to 16 KiB and must contain one compact JWE after trimming
ASCII space, tab, CR and LF only at its edges. Internal wrapping is refused.
The encrypted signed request must carry the mapped principal, binding, target
and Send operation. Email addresses, Date, ReplyTo, HTML, subject, headers,
SPF, DKIM and spam results confer no AIRC authority. No attachment or link is
retrieved, and no plaintext command or extracted reply is executed.

The native signed intent retains its original validity and stable request ID.
Provider arrival time and MessageID cannot renew it. Exact duplicates return
the existing durable result. An expired intent may recover only an exact
retained Send result after cryptographic and current durable authorization
checks; it cannot create or re-execute a job. Conflicts and removed jobs fail.
The existing journal provides these guarantees without a new schema.

The success response is HTTP 200 with fixed JSON `{}` after durable admission
or exact retained recovery. It does not establish successful upstream delivery
and reveals no native receipt or content. Failures are generic HTTP errors.
Postmark retries non-200 responses except 403, which ends retries; its retries
do not extend the original intent's validity. See [the provider retry contract](https://postmarkapp.com/developer/webhooks/inbound-webhook#errors-and-retries).
Use a separately authorized native receipt connector to inspect delivery.

## Bounds and deferred verification

The webhook body is limited to 256 KiB and 16 JSON nesting levels. Shared
connection and request capacity applies. Headers have a two-second read limit;
request reads and writes five seconds; native admission three seconds; idle
connections 30 seconds. TLS 1.3 and HTTP/1.1 or HTTP/2 are supported. Service
shutdown joins handlers before closing the verifier.

Production `go build ./...` passes for Darwin ARM64 and Linux AMD64/ARM64 with
`CGO_ENABLED=0`. Source review, formatting, whitespace and source-size advisory
checks pass. These checks do not establish runtime acceptance.

Deferred acceptance includes real Postmark delivery and TLS trust, exact native
author/target/body, Basic authentication, JSON and address refusals, original
expiry, retained retries, capacity, restart and joined shutdown. No live
Postmark account or AIRC service was contacted during implementation.
