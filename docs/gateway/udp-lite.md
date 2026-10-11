# UDP-Lite

Linux ARM64 IPv4 UDP-Lite was qualified for sends, complete bounded cursor
reads, retained replay and joined shutdown. The listener uses native
protocol-136 sockets with an explicit full-checksum policy and carries plain
author requests in the [UDP packet framing](udp.md), as on every
[network carrier](author-network-carriers.md). Transport checksums never
select an author or establish native AIRC acceptance.

## Endpoint and integrity policy

Configure `udp_lite` through the normal `airc-gateway serve --file CONFIG` path:

```json
{
  "id": "udp-lite",
  "protocol": "udp_lite",
  "address": "127.0.0.1:9011",
  "enabled": true,
  "udp_lite": {"checksum_coverage": "full"},
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

Missing, partial, header-only and differently spelled checksum policies are
rejected. [RFC 3828](https://www.rfc-editor.org/rfc/rfc3828.html) defines wire
coverage zero as protection of the entire packet. The owner sets and reads back
zero for both send and receive coverage, disables broadcast writes, and pins
IPv6 sockets to IPv6. Unsupported platforms or kernels fail visibly without
falling back to ordinary UDP.

Endpoints use canonical numeric IPv4 or IPv6 addresses and decimal ports from
0 through 65535. Port zero permits listener allocation only. Hostnames,
multicast, limited broadcast, mapped IPv4 and noncanonical spellings are
rejected. IPv6 link-local addresses require an explicit bounded interface zone;
other addresses reject unrelated zones. IPv4 link-local addresses remain valid.

TLS and origin options do not apply; the listener access policy does. Replies
go to the request's unicast source within the
[amplification bound](author-network-carriers.md). History and watch require
the ordinary cursor key.

Each [fragment](author-network-carriers.md#fragments) is bounded to 1000
bytes. Reassembly, complete response pages, source admission and delivery uncertainty
follow the [packet contract](udp.md). A transport acknowledgement is never a
native receipt; the retained request and its stable request ID govern retries.

## Native socket and checksum evidence

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

The isolated Linux ARM64 fixture passed three repetitions of the native socket
controls: protocol 136, disabled broadcast, IPv4/IPv6 partial-coverage refusal,
whole Unicode delivery and replies, bounded oversized reads, joined close and
unsafe endpoint/reply refusal. Logs and command/binary provenance remain under
`/tmp/airc-ipv4-multicast-1cb3069/udp-lite-final.log` and
`udp-lite-final-provenance.json`. The checks follow the
[Linux UDP-Lite socket settings](https://docs.kernel.org/networking/udplite.html).

## Combined native Linux qualification

On 2026-10-09, all six current-source integration tests passed together on Linux
ARM64 using independent IPv4 protocol-136 clients and the pinned native AIRC
observer. They verified exact authors, rooms and bodies for Unicode and
fragmented sends; complete one-message cursor pages through exhaustion;
lost-response replay with one send attempt; unverified-input silence;
partial-checksum refusal and recovery; aggregate response capacity and cursor
recovery; active-watch and incomplete-request shutdown; listener-port release;
and protected startup. No tests in this native family skipped.

The host cross-compiled the integration tests and gateway with Go 1.27.1 and
`CGO_ENABLED=0`. The actual run used cached Ubuntu ARM64 image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`,
with network-none, read-only root and artifact bind, no capabilities,
no-new-privileges and disposable local journal state. No package or image was
downloaded. The gateway SHA-256 was
`6671c2116f5cca5f7ba7b75147e811128f178c6a94e85d88d4450f0239957115`;
the in-test digest check matched. The independent native fixture matched its
required baseline and archive hash.

The native run terminated with exit zero. Commands, source digest, binary hashes
and output are retained in
`/tmp/airc-udp-lite-integrated-20261009/provenance.json` and `acceptance.log`.
The source digest was checked before and after execution and independently
recomputed by the primary reviewer. Current-source full normal/race, build,
formatting, vet and staticcheck also passed in the
[shared-memory acceptance checkpoint](testing.md#shared-memory-acceptance-checkpoint).

These results qualify the stated Linux ARM64 IPv4 path. IPv6 has socket-level
evidence only. Linux race instrumentation, other architectures, native AIRC
delivery over IPv6 and Darwin UDP-Lite delivery are not claimed.
