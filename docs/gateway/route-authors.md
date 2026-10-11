# Route authors

Route-owned ingress is ingress whose access is decided outside the gateway: a
broker ACL, a database role, an SSH login, a pinned or signing sender, or a
provider's webhook secret. Like every carrier, it posts as an **author**:
AIRC decides whether the author may post.

## Bound authors and body authors

Carriers whose messages are contract requests (Redis Streams, Redis Pub/Sub,
PostgreSQL NOTIFY, the SFTP mailbox, the SCP inbox) post each request as:

1. the author the request names (`"author":{"nick":...,"token":...}`): with a
   token, that registered account, always (AIRC checks the token); without
   one, a guest, only when the route's `guests` allows;
2. else the route's `bound_author`;
3. else nobody: the request is refused (`invalid_request`).

```json
"bound_author": {"nick": "alerts", "account_token_file": "/owner/alerts.token"},
"guests": false
```

`bound_author.account_token_file` holds an AIRC account token (as `airc user
create` writes one); without it the bound author is a guest under `nick`.
`guests` defaults to true for a route without a bound author and false for one
with a bound author, so binding an author does not silently admit arbitrary
guest nicknames. A route needs a bound author or guests.

Anyone the route's own gate admits (who can PUBLISH, XADD, NOTIFY or write the
mailbox) can post as its bound author; restrict that gate as you would the
account's token. A token in a request crosses the broker as plain text.

## Provider-verified senders

TLS syslog, RFC 5848 signed syslog and SNMPv3 keep their sender checks (pinned
client certificate, origin signature, USM) and post as each grant's required
`bound_author`; their messages are text, not requests.

GitHub webhooks and ChirpStack keep their HMAC and bearer checks. A commenter
or device in the listener's `accounts` map posts as that bound author:

```json
"accounts": [{"login": "octocat", "author_id": 583231, "nick": "octo", "account_token_file": "/owner/octo.token"}]
"accounts": [{"dev_eui": "70b3d57ed0000001", "nick": "front-door"}]
```

Others post as the guest `gh-<login>` or `lora-<deveui>` (bytes outside AIRC's
nickname grammar become `-`, cut to 30 bytes), unless `guests` is false. GitHub
logins can be renamed and reused, so set `author_id` (the immutable numeric user
ID) on a mapped account: a different ID then posts as a guest.

## Source and limits

Route ingress forwards `route:<id>` (the broker, mailbox or listener ID) to
AIRC as its client source, so AIRC's per-source limits apply to the route as a
whole. Each route also has the gateway's flood limits (120 requests per minute
per source, 60 per author); excess is a transient `capacity_exceeded`.

Journal identity is the author scope plus `request_id`, as on every carrier,
so a retry on another carrier deduplicates. Provider replay tables (GitHub,
ChirpStack, SNMP, signed syslog) record the author scope that first used an
event or origin; a later grant to another author conflicts.
