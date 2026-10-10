package store

const migration20 = `
CREATE TABLE matrix_progress (
 owner TEXT PRIMARY KEY CHECK(length(owner)=64 AND owner NOT GLOB '*[^0-9a-f]*'),
 revision INTEGER NOT NULL CHECK(revision>=1),
 cursor TEXT NOT NULL CHECK(length(CAST(cursor AS BLOB))<=1024),
 next_cursor TEXT NOT NULL CHECK(length(CAST(next_cursor AS BLOB))<=1024),
 history_cursor TEXT NOT NULL CHECK(length(CAST(history_cursor AS BLOB))<=1024),
 room_index INTEGER NOT NULL CHECK(room_index BETWEEN 0 AND 128),
 rejected INTEGER NOT NULL CHECK(rejected>=0),
 CHECK((next_cursor='' AND room_index=0 AND history_cursor='') OR (next_cursor<>'' AND next_cursor<>cursor))
) STRICT;
PRAGMA user_version=20;
`
