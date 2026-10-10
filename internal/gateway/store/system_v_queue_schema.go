package store

const migration18 = `
CREATE TABLE system_v_queue_ownership (
 listener TEXT PRIMARY KEY CHECK(length(listener) BETWEEN 1 AND 64 AND listener NOT GLOB '*[^a-zA-Z0-9_-]*'),
 platform TEXT NOT NULL CHECK(platform IN ('darwin-arm64','linux-arm64')),
 queue_key INTEGER NOT NULL UNIQUE CHECK(queue_key BETWEEN 1 AND 2147483647),
 queue_id INTEGER NOT NULL CHECK(queue_id BETWEEN 0 AND 2147483647),
 sequence INTEGER NOT NULL CHECK(sequence BETWEEN 0 AND 65535),
 changed INTEGER NOT NULL CHECK(changed>0),
 boot_id TEXT NOT NULL CHECK(length(boot_id)=36),
 namespace_device INTEGER NOT NULL CHECK(namespace_device>=0),
 namespace_inode INTEGER NOT NULL CHECK(namespace_inode>=0),
 CHECK((platform='darwin-arm64' AND namespace_device=0 AND namespace_inode=0) OR (platform='linux-arm64' AND namespace_inode>0))
) STRICT;
PRAGMA user_version=18;
`
