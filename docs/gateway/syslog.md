# TLS syslog ingestion

## Wire prerequisite

The internal `syslogwire` helper implements a bounded version-1 record profile
from [RFC 5424](https://www.rfc-editor.org/rfc/rfc5424.html), carried using
[RFC 5425 octet counting](https://www.rfc-editor.org/rfc/rfc5425.html).
It is not yet connected to a listener or service. Certificate authorization,
durable dispatch, and actual native qualification remain on the roadmap.

A frame begins with a nonzero decimal byte count and one ASCII space. The record
is limited to 32,768 bytes; an excessive declaration is rejected before allocating
or reading the declared body. The reader consumes exactly that body, preserving
subsequent frames, and returns no partial record on truncation or syntax failure.
Its owner must supply absolute socket deadlines and close after any error; the
helper does not resynchronize an invalid stream or own a connection.

The parser checks priority 0–191 without leading zeros, version 1, the timestamp
or NILVALUE, printable bounded header fields, and complete structured-data
syntax. Timestamp fractions are limited to six digits; invalid dates, leap
seconds, lowercase separators and out-of-range offsets are rejected. Structured
data IDs are unique; repeated parameter names are allowed. Escaped delimiters and
unknown escape sequences are preserved, and parameter values must be valid
shortest UTF-8. This syntax parser does not validate registered structured-data
semantics or private enterprise registrations.

The record owns an exact copy of its input. Structured data and MSG remain byte
slices of that copy. Non-BOM MSG may contain arbitrary bytes; BOM-marked MSG must
contain valid shortest UTF-8. A later send adapter must separately enforce AIRC's
body contract. HOSTNAME, MSGID and timestamps are untrusted metadata and confer
no sender authority, freshness or stable replay identity.

The checkpoint controls exercise one-byte input fragmentation, consecutive frame
boundaries, exact raw-byte ownership, Unicode, repeated parameters, valid and
unknown escapes, binary MSG, malformed syntax, truncated prefixes/bodies,
oversized declarations/header fields, and the exact maximum record size.
`go test -race ./internal/gateway/syslogwire -count=1` passed in 1.278s;
affected vet and staticcheck, formatting and diff checks passed. These are helper
controls, not service/native evidence. Required full acceptance remains due after
executable integration changes.
