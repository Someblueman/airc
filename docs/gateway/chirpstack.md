# LoRaWAN unconfirmed uplinks through ChirpStack 4

The `chirpstack` listener accepts ChirpStack 4 HTTP integration `up` events
and submits a small UTF-8 message as the configured native AIRC principal.
This selects the [ChirpStack HTTP JSON integration](https://www.chirpstack.io/docs/chirpstack/integrations/http.html)
for the LoRaWAN catalogue entry. It is a Send-only integration: radio devices
do not receive a native reply, cursor or history page through this path.
Implementation review and builds are separate from deferred radio acceptance.

## Trust and provisioning

Use a trusted ChirpStack 4 instance with activated devices and normal LoRaWAN
MIC and frame-counter validation. Keep each device's `skip_fcnt_check` disabled;
the [ChirpStack device API](https://github.com/chirpstack/chirpstack/blob/master/api/proto/api/device.proto)
warns that enabling it allows replay attacks. Secure device session/root keys,
gateway-to-server access and application integration administration. The
gateway delegates radio authentication and decryption to that server.

Configure the ChirpStack application's HTTP integration for JSON encoding,
an HTTPS endpoint ending in `/v1/chirpstack/events` with no existing query,
and a static `Authorization: Bearer SECRET` header. ChirpStack adds
`?event=up` for uplinks. The secret file contains 32–128 ASCII letters, digits,
hyphens or underscores with no newline, is a regular owner-only private file,
and must be unique to this gateway integration. The TLS certificate must be
trusted by the server; the listener requires TLS 1.3 or newer.

Only the exact POST `/v1/chirpstack/events?event=up` is admitted. Other event
types, confirmed uplinks, alternate query strings, browser origins, encoded
paths, compressed bodies and multiple authorization headers are refused.
ChirpStack also posts other event types; those refusals can appear in its
integration logs and do not submit messages.

Map the exact tenant UUID, application UUID, lowercase 16-digit DevEUI and
application FPort (1–223) to one active Send principal and fixed native target.
Device names, tags, gateway IDs, display names and payload contents cannot
choose an AIRC identity or target. The server's credential authorizes these
mapped grants; there is no additional application signature from the device.
Deploy this integration only when that delegation is intended.

Within an otherwise provisioned configuration:

```json
{
  "id": "lorawan",
  "protocol": "chirpstack",
  "address": "0.0.0.0:8443",
  "enabled": true,
  "tls_cert_file": "/absolute/keys/gateway-chain.pem",
  "tls_key_file": "/absolute/keys/gateway-key.pem",
  "operations": ["send"],
  "chirpstack": {
    "secret_file": "/absolute/keys/chirpstack-webhook.secret",
    "server_id": "8742b92b-6e77-44a1-9711-a24a15a98315",
    "freshness_seconds": 120,
    "routes": [{
      "tenant_id": "52f14cd4-c6f1-4fbd-8f87-4025e1d49242",
      "application_id": "17c82e96-be03-4f38-aef3-f83d48582d97",
      "dev_eui": "0101010101010101",
      "f_port": 1,
      "principal": "sensor",
      "target": {"kind": "room", "name": "#sensors"}
    }]
  }
}
```

Provision a stable nonzero UUID for `server_id`; it namespaces event IDs for
that ChirpStack deployment and must survive restarts and secret rotation.
The other UUIDs and DevEUI come from the actual server. Up to 128 routes are
allowed per listener, and every route must match current principal grants.
There are no return routes or anonymous principals. Start with
`airc-gateway serve --file CONFIG`.

## Device payload and admission

Send 1–242 UTF-8 bytes as an unconfirmed application uplink. Its actual allowed
radio length depends on region, data rate and LoRaWAN overhead; this application
cap is not permission to exceed those limits. The bytes are the exact message,
not JSON and not a request envelope. They must be nonblank and remain unchanged
by native message normalization. The
[uplink JSON event](https://www.chirpstack.io/docs/chirpstack/integrations/events.html)
carries these decrypted bytes in padded standard base64 `data`. The listener
rejects invalid base64, invalid UTF-8, oversized messages and MAC-command ports.

Events have a 64 KiB JSON bound and depth limit, with duplicate keys, null
admission fields and case aliases rejected. Unused provider metadata is ignored
within those bounds. Omitted protobuf scalar defaults are accepted: missing
`confirmed` means false and missing `fCnt` means zero. Exact device route and
current native grants are checked before admission.

Freshness uses the original event `time`, never a new arrival timestamp. The
configured interval is 1–300 seconds; future and expired events are refused.
ChirpStack can derive this timestamp from radio-gateway GPS/clock metadata,
falling back to server time. Trust and synchronize those clocks. This bounds
the server-reported event age; it does not prove a cryptographic device creation
time. Reposting the same event cannot renew its original deadline.

The durable request ID hashes the stable server namespace and
`deduplicationId`. Its retained fingerprint binds device, port, frame counter,
original time, message and native principal/binding/target. Repeated identical
events return the retained result; conflicting reuse is rejected. Event binding
and native intake commit atomically, with at most 16,384 retained event records.
Expired records are pruned only after the associated native job is gone. Store
schema 22 adds this table transactionally while retaining existing state.

HTTP 202 reports durable gateway admission and the retained native result;
it is not proof that a radio device saw an acknowledgement or that AIRC delivery
has completed. Shared request/connection limits and bounded HTTP timeouts apply.
Shutdown stops intake and joins handlers before clearing credentials.

ChirpStack's [HTTP sender](https://github.com/chirpstack/chirpstack/blob/master/chirpstack/src/integration/http.rs)
logs failed posts without retrying them. Gateway deduplication handles repeated
copies of the same event, but does not recover events lost before intake.
A newly transmitted uplink receives a new server event ID and represents a
new message, even if its text matches. This unconfirmed radio path makes no
end-to-end reliable-delivery claim.

## Deferred verification

Production `go build ./...` passed with `CGO_ENABLED=0` for Darwin ARM64,
Linux AMD64 and Linux ARM64. Formatting, whitespace and the advisory source
size check passed. Source review covered provider behavior, exact grants,
bounded parsing, original freshness, atomic retention and service ownership.

No ChirpStack deployment, radio device, HTTP fixture, migration execution,
native AIRC test, normal/race suite or full acceptance gate was run. Later
acceptance requires a provisioned server/device and independent AIRC observer:
verify device MIC/counter policy, author/target/body, tenant/application/port
refusals, authentication, original freshness, duplicate/conflicting events,
retained replay across restart and migration, body/capacity bounds, pre-intake
loss, revoked grants and joined shutdown.
