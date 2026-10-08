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

The private `MatrixBridge` configuration validator requires one concrete
numeric-IP dial endpoint with nonzero port, a verified TLS server name and
optional absolute CA path, an exact bridge account and absolute access-token
path. It bounds freshness provisioning to 1–300 seconds and grants to 1–128
distinct exact author/room pairs. Each grant must name an active existing
principal with the exact target and distinct explicitly authorized send,
context, history or search operations; history/search require a cursor key.
Bridge-account grants and room aliases are refused. Case-distinct opaque rooms
remain distinct grants. This configuration is not registered with the service;
file contents/permissions, actual verifier ownership, homeserver authentication
and reply disclosure are not established by configuration validation.
