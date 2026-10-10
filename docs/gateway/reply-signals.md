# Reply signals

`reply_signal.set` and `reply_signal.cancel` use native CHAT prepare/cancel on
HTTP/1.1, HTTP/2, framed TCP/TLS, WebSocket, Unix streams and stdio. Set requires
`target`, `parent_id`, and `reply_signal: {"note": "coming soon", "ttl_seconds": 120}`.
TTL is 1–900 seconds and note is at most 240 bytes of valid brief text. Cancel
requires the target and parent but rejects a reply_signal object. Both support
bounded retry by default and explicit receipt-only policy.

Before any write, a bounded same-session context query proves the retained
parent belongs to the asserted target. Room proof requires the exact room.
Direct proof requires the parent sender/destination pair to match the configured
account nickname and permitted peer; self-DM is supported. Incoming direct
parents can have the account nickname as their actual native target. The
acknowledgement preserves that raw target, including casing, separately from
the normalized requested target. Missing, evicted, forged or malformed proof
prevents a write. Native direct participation is defined by nicknames; this is
not proof of historical recipient account identity and does not enable DM reads.

A complete native 777 acknowledgement plus 778 terminator yields typed
`reply_signal` evidence containing operation, requested target, parent ID,
actual native target, action, sender, account ID, note and expiry. Prepare
includes the authenticated account ID. Cancel has empty account ID/note and a
nonzero native timestamp. No new message ID, message persistence or recipient
connection claim is made. The native two-second prepare throttle can return
an earlier note/expiry, which the gateway preserves rather than replacing with
the requested values. Cancelling a retained parent with no signal still succeeds.
Native mute/permission rejection remains a permanent failure.

Signals are native in-memory state. Gateway acknowledgements are durable
historical evidence, not a current waiting snapshot. Replaying an accepted
request returns the saved whole acknowledgement without writing, even after
expiry or an intervening native change. After an uncertain write, retry can
refresh prepare or rebroadcast cancel; results expose possible duplicate risk.
There is no native request-ID lookup, so both operations use zero lookup attempts.
Receipt-only recovery makes no second write.

Native fixtures observe independent 780 broadcasts, whole acknowledgements,
HTTP profiles and all implemented stream carriers, throttle, incoming/outgoing/
self direct parents, forged room/direct IDs, mute recovery, no-op cancel,
subprocess restart replay and journal crash recovery under both delivery policies.
Gateway forwarding of nonmessage live events remains pending. The complete
72-entry catalogue remains outstanding; additional native operations are optional.
