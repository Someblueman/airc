-- Apply only to a new, empty private mailbox.sqlite, with a client that
-- stops and rolls back on any SQL error. Do not apply to a live mailbox.
PRAGMA page_size = 4096;
PRAGMA journal_mode = DELETE;
PRAGMA synchronous = FULL;
PRAGMA trusted_schema = OFF;
BEGIN IMMEDIATE;
CREATE TABLE mailbox (slot INTEGER PRIMARY KEY CHECK (slot = 1), request BLOB NOT NULL CHECK (length(request) BETWEEN 1 AND 16384), result BLOB CHECK (result IS NULL OR length(result) BETWEEN 1 AND 65536)) STRICT;
PRAGMA application_id = 0x4149524d;
PRAGMA user_version = 1;
COMMIT;
