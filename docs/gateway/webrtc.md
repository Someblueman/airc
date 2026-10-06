# WebRTC DataChannels

The `webrtc` listener uses HTTPS signaling and a single authenticated WebRTC
DataChannel per peer. Signaling requires a current bearer principal; every
operation also requires its own signed or encrypted envelope for that same
principal and AIRC binding. ICE addresses and DTLS fingerprints establish
transport connectivity, not gateway authority. Durable revocation is checked
at signaling and operation dispatch.

Enable a listener with its explicit operation allowlist, TLS certificate/key,
and browser origin allowlist. Optional `webrtc` settings contain
`ice_transport_policy` (`all` or `relay`) and `ice_servers`. Each ICE server has
`urls`; TURN servers additionally require `username` and an owner-only
`credential_file`. A relay policy requires a configured TURN server. Host ICE
needs no third-party account. Configure your own STUN/TURN deployment when
network topology requires it; local loopback acceptance alone does not prove
remote NAT traversal.

Signaling is `POST /v1/webrtc/offer` with bearer authentication and strict JSON:

```json
{"version":1,"sdp":"<gathered offer>","mode":"reliable_ordered"}
```

The response contains `version:1` and the gathered answer `sdp`. Trickle ICE is
not used. Offers contain one data-only application media section. The peer
opens one channel named `airc-gateway-v1`, with protocol `agd1`.

| Mode | Ordered | Retransmission setting |
| --- | --- | --- |
| `reliable_ordered` | Yes | Default reliable |
| `reliable_unordered` | No | Default reliable |
| `unreliable_ordered` | Yes | `maxRetransmits:0` |
| `unreliable_unordered` | No | `maxRetransmits:0` |

All modes carry the same operations, including history, replies, receipts,
and retained/live watches. Responses have correlation IDs and sequences.
Clients advance a watch cursor only through contiguous, complete pages.
Unreliable modes can lose any fragment or response; transport send success
does not prove intake or native acceptance. Preserve durable request IDs
through bounded retries and use `receipt.get` to recover uncertain results.
Possible duplicates remain part of the configured delivery policy.

Each binary DataChannel message carries one AGD1 fragment, at most 1000 bytes.
Requests assemble to at most 16 KiB; responses to at most 64 KiB. Fragmentation
allows larger history responses without assuming a browser's SCTP message
limit. Assembly and replay state are bounded. Per-peer active requests,
global request admission, peer counts, and per-principal peers are bounded;
watch and upstream session budgets remain separately enforced.

The browser example [envelopes-webrtc.js](../../examples/gateway/envelopes-webrtc.js)
exports `connectWebRTC(base, {bearer, mode, iceServers, iceTransportPolicy, signal})`.
Its `call(profile, envelope)` returns a correlated response; `watch` yields
complete pages with native `body_base64` metadata; use `messageText` from
[events-fetch.js](../../examples/gateway/events-fetch.js) to decode UTF-8 bodies. Cancellation closes the peer and its concurrent operations.
A loss or incomplete watch leaves the last confirmed cursor on the error.
Sign/encrypt fresh envelopes while retaining the logical mutation request ID
when retrying; correlation and fragment IDs are connection-local.

Implementation and acceptance evidence are recorded in [STATUS.md](STATUS.md).

## Native mode qualification

Reliable ordered and reliable unordered native DataChannel fixtures check the
opened channel's corresponding ordered setting and absence of retransmission or
packet-lifetime limits. Two
encrypted, authenticated sends produce durable accepted receipts with one native
attempt each. Independent native CHECK records verify IDs, author, target and
whole multiline Unicode bodies. Consecutive one-message history pages match the
complete native records, report `more` then `ok`, and retain nonempty cursors
without gaps; the caught-up cursor returns an empty page.

Both reliable-mode qualifications pass twice under the race detector. The
reliable ordered qualification and existing four-mode send/read matrix also pass twice
under `go test -race` on Darwin. `go vet ./integration/gateway` passes. These
local Go-client checks do not establish browser certificate trust or remote
NAT/relay acceptance; those remain on the roadmap.
