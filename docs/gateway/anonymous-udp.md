# Anonymous UDP

An explicitly anonymous UDP listener accepts clients without credentials,
enrollment, a configured client address, or a fixed client port. The gateway
forwards their requests through one operator-selected AIRC account and replies
to the source IP and port of each datagram. The native server still speaks its
existing protocol; only the gateway handles UDP.

This mode supports `send`, `receipt.get`, and `history.read`. Other UDP listeners
continue to use the authenticated [AGD1 profile](udp.md). Anonymous mode is
plaintext: neither the sender nor the response is authenticated.

## Operator configuration

Set `anonymous_principal` on a `protocol: "udp"` listener to an existing principal
ID. That principal supplies the server-side native binding, allowed operations,
and targets. It needs no `bearer_token_file` or `signing_keys`. For example,
these entries in an otherwise complete gateway configuration expose one room:

```json
{
  "principals": [{
    "id": "guest",
    "binding": "guest-native",
    "operations": ["send", "receipt.get", "history.read"],
    "targets": [{"kind": "room", "name": "#guest-lab"}]
  }],
  "listeners": [{
    "id": "udp",
    "protocol": "udp",
    "address": "127.0.0.1:9002",
    "enabled": true,
    "anonymous_principal": "guest",
    "operations": ["send", "receipt.get", "history.read"]
  }],
  "return_routes": []
}
```

The operator still configures `guest-native` with the native server endpoint,
account, and its private token. These stay on the gateway host. Keep the normal
journal, retry, limit, and cursor-key configuration. A solely anonymous gateway
needs no JOSE recipient keys or response signing key. Do not attach return routes
to an anonymous listener; configuration validation rejects them.

Use a reachable interface address and allow that UDP port for clients on another
machine. Loopback in the example only accepts clients on the gateway machine.
Anyone who can reach the listener gets the guest's configured permissions.
All clients share its author, retained receipts, history, capacity, and request-ID
namespace; use unpredictable, unique IDs for new sends. Clients cannot nominate
another principal, native author, or backend. Revoking the shared principal
disables anonymous access.

Changing an existing listener to anonymous mode changes its wire protocol.
Restart the gateway and switch its clients together. Keeping the same realm,
principal ID, and binding preserves the existing journal. This does not remove
the separate restriction on adding identities to an initialized journal.

## Client

Build once from the repository root:

```sh
go build -o bin/udp-anonymous-client ./examples/gateway/udp-anonymous-client
printf '%s\n' '{"version":1,"request_id":"example-unique-send","operation":"send","target":{"kind":"room","name":"#guest-lab"},"message":"Hello without credentials"}' \
  | bin/udp-anonymous-client --address 127.0.0.1:9002
```

No special SDK is required. Python's standard library can send the same request:

```python
import json
import socket
import uuid

request = {
    "version": 1, "request_id": uuid.uuid4().hex, "operation": "send",
    "target": {"kind": "room", "name": "#guest-lab"},
    "message": "Hello without credentials",
}
with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
    client.settimeout(5)
    client.connect(("127.0.0.1", 9002))
    client.send(json.dumps(request).encode().ljust(4096, b" "))
    print(client.recv(4096).decode())
```

Both clients open a new ephemeral UDP socket for each call and read no credential
files. A successful admission returns `result.result`; poll `receipt.get` with
`lookup_id` set to the send's request ID until its state is `accepted` or terminal.
For `history.read`, use the same room target, `start: "retained"` or `"recent"`,
and a small `limit`. Messages and the next opaque cursor are in `result.page`;
message bodies use `body_base64` (base64url). See the [guest runbook](guest-test.md)
for complete send, receipt, and history commands.

## Wire and bounds

Each request is one UTF-8 JSON `core.Request` datagram. Each response is one JSON
object with `version: 1`, the request's `request_id`, and exactly one of `result`
or `error`. No signature, encryption envelope, AGD1 framing, or handshake is used.
Unknown/duplicate fields, invalid requests, and oversized datagrams are discarded.

Both datagrams are limited to 16 KiB, and the response never exceeds the request's
wire length. Append JSON whitespace to reserve response room. The Go client
pads to 4096 bytes by default; `--max-response-bytes` accepts 512–16384. Host UDP
limits may be lower. A response that does not fit becomes `capacity_exceeded`;
increase the budget or request fewer history messages. If even that error does
not fit, there is no reply. Processing is serial per listener with a four-second
dispatch deadline and the service's shared request capacity.

Large datagrams may fragment on the network. UDP can lose requests or replies;
after a timeout, retry the same JSON intent and request ID. The durable journal
deduplicates within its configured retention period, including across gateway
restarts. A changed intent under the same ID is rejected. A timeout or
`capacity_exceeded` response does not prove a send was rejected: retrieve its
receipt or retry the unchanged request. The example client does not retry
automatically. This mode adds no reliable delivery or per-client fairness.
