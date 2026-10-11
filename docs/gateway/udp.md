# UDP unicast

The `udp` listener carries plain contract requests in datagram fragments and
answers each request's source; see
[author network carriers](author-network-carriers.md) for the shared request
form, fragment format and reflection bound. A request names its author
(`{"nick"}` or `{"nick","token"}`); there is no connection, so there is no
identify. Tokens travel in clear, as on any cleartext IRC connection.

## Configuration

```json
{"id": "udp", "protocol": "udp", "enabled": true, "address": "0.0.0.0:6688"}
```

The address is a numeric IP and port; a wildcard address and port `0` are
allowed. TLS and origin fields are rejected. The listener's access gate
applies ([configuration.md](configuration.md#listeners)); the client's IP
address is its source for rate limits and aircd's `WEBIRC`.

## Wire and limits

Each fragment is at most 1000 bytes. Requests reassemble to at most 16 KiB
and result frames to 64 KiB. A reply to a non-loopback source may use at most
three times the request's wire bytes plus a 512-byte final error frame, so
a spoofed source cannot turn the listener into an amplifier; pad a request
with JSON whitespace to make room for a larger page. Four incomplete
assemblies are kept per source and expire after five seconds. Writes are
serialized and bounded by three seconds. Calls last at most four seconds and
streamed watches twenty. Inactive sources expire after five seconds. Invalid
input is dropped without a reply. Shutdown closes the socket and joins active
requests.

Every implemented operation can use this path. A retry keeps the original
request ID and intent; transport correlation and fragment IDs may change
after loss. A datagram's arrival is not native acceptance: read the receipt.
UDP loss grants no new attempt budget and does not extend expiry.

## Evidence

```sh
go test -race -count=1 ./integration/gateway -run '^TestUDP|TestAuthorNetworkCarriers'
```

Loopback tests cover native sends and reads, receipts, watches with shutdown,
fragment reassembly, malformed, oversized and incomplete input, and capacity.
The example in [author-network-carriers.md](author-network-carriers.md#evidence)
posted from a real client to a scratch aircd.
