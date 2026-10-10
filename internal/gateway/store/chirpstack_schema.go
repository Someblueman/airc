package store

const migration22 = `
CREATE TABLE chirpstack_events (
 request_id TEXT PRIMARY KEY CHECK(length(request_id)=64),
 fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 principal TEXT NOT NULL, binding TEXT NOT NULL,
 expires_seconds INTEGER NOT NULL CHECK(expires_seconds>=0),
 expires_nanos INTEGER NOT NULL CHECK(expires_nanos BETWEEN 0 AND 999999999)
) STRICT;
PRAGMA user_version=22;
`
