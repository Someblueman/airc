package store

// Widen the platform constraint without changing any retained lifecycle value.
// initialize runs the rebuild and user_version update in one transaction.
const migration23 = `
ALTER TABLE system_v_queue_ownership RENAME TO system_v_queue_ownership_v22;
CREATE TABLE system_v_queue_ownership (
 listener TEXT PRIMARY KEY CHECK(length(listener) BETWEEN 1 AND 64 AND listener NOT GLOB '*[^a-zA-Z0-9_-]*'),
 platform TEXT NOT NULL CHECK(platform IN ('darwin-arm64','linux-arm64','linux-amd64')),
 queue_key INTEGER NOT NULL UNIQUE CHECK(queue_key BETWEEN 1 AND 2147483647),
 queue_id INTEGER NOT NULL CHECK(queue_id BETWEEN 0 AND 2147483647),
 sequence INTEGER NOT NULL CHECK(sequence BETWEEN 0 AND 65535),
 changed INTEGER NOT NULL CHECK(changed>0),
 boot_id TEXT NOT NULL CHECK(length(boot_id)=36),
 namespace_device INTEGER NOT NULL CHECK(namespace_device>=0),
 namespace_inode INTEGER NOT NULL CHECK(namespace_inode>=0),
 CHECK((platform='darwin-arm64' AND namespace_device=0 AND namespace_inode=0) OR (platform IN ('linux-arm64','linux-amd64') AND namespace_inode>0))
) STRICT;
INSERT INTO system_v_queue_ownership
 (listener,platform,queue_key,queue_id,sequence,changed,boot_id,namespace_device,namespace_inode)
 SELECT listener,platform,queue_key,queue_id,sequence,changed,boot_id,namespace_device,namespace_inode
 FROM system_v_queue_ownership_v22;
DROP TABLE system_v_queue_ownership_v22;
PRAGMA user_version=23;
`
