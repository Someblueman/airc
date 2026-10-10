# Twilio inbound SMS

The selected provider is **Twilio Programmable Messaging**, using its inbound
SMS HTTPS form webhook. The `twilio_sms` listener accepts an encrypted, signed
AIRC Send intent in the SMS body. It is send-only. Implementation is available;
testing and deployment are deferred.

## Configuration

Add this listener to an existing gateway configuration:

```json
{
  "id": "sms-ingress",
  "protocol": "twilio_sms",
  "address": "0.0.0.0:8443",
  "enabled": true,
  "tls_cert_file": "/absolute/private/server-cert.pem",
  "tls_key_file": "/absolute/private/server-key.pem",
  "operations": ["send"],
  "twilio_sms": {
    "account_sid": "YOUR_TWILIO_ACCOUNT_SID",
    "auth_token_file": "/absolute/private/twilio-auth-token",
    "webhook_url": "https://sms.example.com/v1/twilio/sms",
    "routes": [
      {
        "from": "+12025550101",
        "to": "+12025550102",
        "principal": "sms-author",
        "target": {"kind": "room", "name": "#engineering"}
      }
    ]
  }
}
```

Replace the example account SID, phone numbers and endpoint with provisioned
values. Each exact From/To pair maps to one active signing principal, its
existing binding and an explicitly granted Send target. Configure 1–128 routes.
Numbers use canonical international `+` and digits, without punctuation.
The gateway requires its existing P-256 ingress decryption key and the
principal's trusted signing public key. No response signing key is required.

The account Auth Token file must be an owner-private regular file containing
32–128 printable ASCII bytes, without a newline or trimming. Startup retains
the token until the HTTP handlers have joined, then clears its buffer. The
service neither provisions Twilio resources nor sends outbound SMS messages.

Configure the Twilio number's incoming-message webhook as POST to the exact
`webhook_url`. This profile requires a lowercase DNS name, HTTPS on the public
default port, and the literal path `/v1/twilio/sms`; explicit ports, query
parameters, fragments and alternate encodings are refused. A private listener
such as port 8443 needs an authorized forwarding path from public port 443.
The gateway connection must itself use TLS and preserve the exact public Host
and request path. Forwarded headers never select signature authority. Provision
a certificate and path trusted by Twilio; listener setup does not provide them.

## Provider and author authentication

Twilio signs the exact configured public URL followed by the sorted decoded
POST parameter names and values using HMAC-SHA1 with the account Auth Token.
The listener compares a single canonical `X-Twilio-Signature` in constant time.
See [Twilio's signature contract](https://www.twilio.com/docs/usage/security)
and [incoming-message fields](https://www.twilio.com/docs/messaging/guides/webhook-request).

The request must be `application/x-www-form-urlencoded`, optionally with
`charset=utf-8`. Compressed bodies, Origin headers, duplicate parameters or
case aliases, malformed escapes and non-UTF-8 values are refused. Additional
bounded parameters participate in the signature even when unused by this
profile. AccountSid must match configuration, MessageSid must be an SMS SID,
and NumMedia must be `0`. Optional SmsSid/SmsMessageSid aliases must match;
optional SmsStatus/MessageStatus values must be `received`. MMS and unmapped
address pairs are refused; no media URL is fetched.

The SMS Body must contain exactly one compact JWE, at most 1600 ASCII bytes,
carrying the existing signed AIRC request. Its principal, binding, target and
Send operation must match the configured phone route. Provider provenance and
phone numbers alone cannot authorize a native operation. The whole token must
fit; whitespace, split application envelopes and plaintext commands are not
accepted. The client must account for envelope overhead within this limit.

The signed intent's original validity window and native request ID govern
freshness and replay. Neither webhook arrival nor MessageSid renews freshness
or supplies native request identity. Exact redelivery reuses the durable job.
After expiry, a cryptographically valid exact retained Send may recover only
its existing result, subject to current durable authorization; it cannot create
or re-execute a job. Removed jobs and conflicting requests cannot be recovered.
This uses the existing journal and adds no durable schema.

HTTP 200 with fixed TwiML `<Response/>` means the request has a durable gateway
result. It does not assert successful upstream AIRC delivery, and contains no
receipt or message content. The empty response requests no outgoing SMS.
Failures return generic HTTP errors. Existing native receipt mechanisms remain
available through separately authorized connectors. Provider retry policy is
a deployment choice and does not alter the original intent's deadline.

## Bounds and deferred verification

Forms are limited to 16 KiB, 64 fields, 64 bytes per name and 2048 bytes per
decoded value. Shared connection and request limits apply. Header reads are
limited to two seconds, request reads and writes to five seconds, admission
to three seconds and idle connections to 30 seconds. The listener uses TLS 1.3
and HTTP/1.1 or HTTP/2; service shutdown joins handlers before releasing keys.

Production `go build ./...` passes for Darwin ARM64 and Linux AMD64/ARM64 with
`CGO_ENABLED=0`. Source review, formatting and whitespace checks also pass;
these do not establish runtime acceptance. Deferred verification includes real Twilio delivery
and TLS trust, exact native author/target/body, signature/form boundaries,
original expiry, retained redelivery, capacity, restart and joined shutdown.
No Twilio account, phone number or live AIRC service was contacted to implement
this mode.
