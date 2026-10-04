# Gateway request contract v1

This contract is implemented by `internal/gateway/core`. It defines normalized
intent, not an enabled listener or supported-adapter claim. Dispatchers must
separately authenticate, authorize, intersect implemented capabilities, and
durably admit mutations before returning a queued result.

An envelope is one UTF-8 JSON object, at most 16 KiB. Reject unknown or duplicate
keys (including escaped duplicate names), case aliases, null fields, unknown
versions, excess nesting, and trailing JSON values. Adapters must bound input
before allocating it. No caller-supplied identity, upstream address, credential,
or arbitrary return route is part of this contract.

Common required fields are `version` (integer 1), `request_id` (1–64 ASCII
letters, digits, hyphens or underscores), and `operation` (exact enum below).
Targets have exactly `kind` and `name`: `room` preserves case and accepts the
pinned AIRC channel grammar (2–64 UTF-8 bytes, initial `#` or `&`, no controls,
space, comma, or colon); `direct` uses the pinned 1–30-byte ASCII nickname
grammar and normalizes to lowercase. Audit selectors and multi-target strings
are never valid targets.

| Operation | Required fields | Other allowed fields |
|---|---|---|
| `send` | target, nonblank message | issued_at, expires_at, delivery_policy |
| `reply` | target, nonblank message, parent_id | issued_at, expires_at, delivery_policy |
| `reaction.add` | room target, parent_id, valid reaction | issued_at, expires_at, delivery_policy |
| `receipt.get` | lookup_id | none |
| `history.read`, `watch` | target, limit (1–1000) | cursor |
| `search` | target, limit (1–1000), nonblank query | cursor |
| `directory` | limit (1–256) | query (one nickname; omitted means whole public snapshot) |

Mutation message bodies normalize CRLF and CR to LF, reject NUL, and must fit
4096 UTF-8 bytes after normalization. Reply parents are 32-character hexadecimal
AIRC message IDs. Declaring a target on a reply is an authorization assertion;
the backend must verify the retained parent's actual room or DM participants
before submitting. Receipt lookup IDs use the same grammar as request IDs and
remain scoped to the verified principal/binding. Queries fit 256 UTF-8 bytes
without CR/LF/NUL; cursors fit 2048 bytes and require separate authenticated,
principal/binding/selector-bound validation in the read implementation.

Room reactions use the native built-in/custom reaction grammar, without a
message body; see [reactions.md](reactions.md) for parent proof, feature
negotiation and the native receipt/retry boundary.

Directory query uses the same ASCII nickname grammar as a direct target and
normalizes to lowercase. It remains a public-global directory operation with no
chat target assertion; `query` is not a wildcard or substring filter. Existing
request encodings and durable mutation fingerprints remain unchanged.

Mutation times use integer Unix seconds, never floating-point values. Nonnegative
explicit expiry must follow explicit issue time. Admission must apply validity,
clock skew, and configured maximum-lifetime policy. `delivery_policy` accepts
`retry` or `receipt_only`; omitted policy defaults to `retry` at first admission.
Omitted times are resolved once at first admission, with the default 24-hour
lifetime. Signed profiles must provide authenticated explicit validity.

Fingerprint v1 is the lowercase hexadecimal SHA-256 of the bytes
`airc-gateway-intent-v1` followed by NUL followed by Go `encoding/json`'s compact
encoding of the normalized `core.Request` struct in declared field order, with
`request_id` set to the empty string. Zero optional fields are omitted according
to their declared JSON tags; explicit pointer-valued defaults remain present.
This encoding is frozen by a golden vector. Identity is not part of content.
Omitted defaults are part of explicit intent: admission must atomically find a
prior request before resolving fresh defaults, then reuse its stored effective
policy and lifetime. Reusing an ID with changed intent is a conflict. The store
must additionally persist effective defaults and immutable backend binding;
the parser alone does not provide deduplication or durable responsibility.

Upstream request identity is the lowercase hexadecimal SHA-256 of
`airc-gateway-upstream-id-v1` followed by NUL followed by compact JSON encoding
of the four-element string array `[realm, binding, verified_principal, request_id]`.
Each scope component is required. The resulting 64-character ID fits AIRC's
request grammar. Credentials and transient socket addresses are excluded so
credential rotation does not change authorship or operation identity.

Other AIRC operations remain required wherever transports support them. Add
their explicit schemas, authorization and acceptance evidence before advertising
them; this initial operation schema is a development step, not reduced scope.

## Results and capability intersection

`core.Result` defines a versioned response with `request_id`, `state`, `durable`,
optional `message_id`, `accepted`, `persisted`, `recipient_connected`,
`duplicate_risk`, `acceptance_uncertain`, separate `send_attempts` and `lookup_attempts`, integer
`expires_at`, optional `next_attempt_at`, `next_action`, and optional `error`.
Receipt facts are nullable booleans: null means unknown, distinct from false.
Connected never means read. These types define a schema, not an implemented
delivery state machine or acceptance claim.

States are `queued`, `dispatching`, `retry_wait`, `uncertain`, `accepted`,
`rejected`, `expired`, and `blocked`. Error objects contain a stable `code` and
`transient` boolean and optional numeric `upstream_code`. Codes are `authentication_failed`, `forbidden`,
`unsupported_operation`, `invalid_request`, `id_conflict`, `capacity_exceeded`,
`upstream_unavailable`, and `delivery_uncertain`. Later components must map
actual evidence into these outcomes; they cannot infer acceptance from a write.

Effective operations intersect trusted implementation, instance configuration,
principal permissions, and upstream support. Any absent set denies everything.
The caller cannot supply these sets. Duplicate or unknown operation names
cannot expand the result. The local CLI advertises compiled intake and configured instances without
starting them; see [configuration.md](configuration.md). This is not negotiated
upstream readiness or a complete catalogue claim.
