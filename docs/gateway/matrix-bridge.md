# Matrix bridge profile

Private bounded identifier helpers retain exact user, room and event values.
User localparts follow the current grammar; server names support ASCII DNS,
IPv4 and bracketed IPv6 with optional ports 1–65535, without zones or IDNA
conversion. Opaque room/event IDs preserve case and Unicode, including legacy
and domainless forms. The selected 255-byte profile refuses controls and
whitespace; room aliases never establish room authorization. See the
[Matrix identifier specification](https://spec.matrix.org/latest/appendices/#identifier-grammar).

These are parsed data, not identity proofs. Verified homeserver provisioning,
account and membership checks, explicit author/room grants, loop prevention,
freshness, durable admission, bounded replies and real-client/native
qualification remain on the roadmap. No service consumes these helpers yet.
