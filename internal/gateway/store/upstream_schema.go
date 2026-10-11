package store

import "strings"

// migration25 drops UNIQUE from jobs.upstream_id. New jobs send AIRC the
// client's own request ID (AIRC scopes it per author), so two authors may use
// the same one; identity stays the (author_scope, request_id) primary key.
// Rows are copied unchanged: a job journaled earlier keeps its stored v2
// upstream ID, so its retries and lookups still find what it may have posted.
// SQLite cannot drop a column constraint, so the table is rebuilt from
// migration24's definition and its indexes and triggers are recreated.
var migration25 = func() string {
	table := migration24[strings.Index(migration24, "CREATE TABLE jobs ("):]
	rest := table[strings.Index(table, ") STRICT;\n")+len(") STRICT;\n"):]
	table = table[:len(table)-len(rest)]
	rest = rest[:strings.Index(rest, "CREATE TABLE replay (")]
	table = strings.Replace(table, "CREATE TABLE jobs (", "CREATE TABLE jobs_next (", 1)
	table = strings.Replace(table, "upstream_id TEXT NOT NULL UNIQUE,", "upstream_id TEXT NOT NULL,", 1)
	return table + `INSERT INTO jobs_next SELECT * FROM jobs;
DROP TABLE jobs;
ALTER TABLE jobs_next RENAME TO jobs;
` + rest + "PRAGMA user_version=25;\n"
}()
