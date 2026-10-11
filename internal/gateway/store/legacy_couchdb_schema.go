package store

// Historical schema only: CouchDB change-feed ingress has been removed. Keep
// this table and its checkpoints so existing journals open unchanged.
const migration21 = `
CREATE TABLE couchdb_progress (
 namespace TEXT PRIMARY KEY CHECK(length(namespace)=36 AND substr(namespace,9,1)='-' AND substr(namespace,14,1)='-' AND substr(namespace,19,1)='-' AND substr(namespace,24,1)='-' AND substr(namespace,1,8) NOT GLOB '*[^0-9a-f]*' AND substr(namespace,10,4) NOT GLOB '*[^0-9a-f]*' AND substr(namespace,15,4) NOT GLOB '*[^0-9a-f]*' AND substr(namespace,20,4) NOT GLOB '*[^0-9a-f]*' AND substr(namespace,25,12) NOT GLOB '*[^0-9a-f]*'),
 scope TEXT NOT NULL CHECK(length(scope)=64 AND scope NOT GLOB '*[^0-9a-f]*'),
 revision INTEGER NOT NULL CHECK(revision>=1),
 sequence TEXT NOT NULL CHECK(length(CAST(sequence AS BLOB)) BETWEEN 1 AND 2048)
) STRICT;
PRAGMA user_version=21;
`
