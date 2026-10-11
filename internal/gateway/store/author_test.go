//go:build darwin || linux

package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

const authorToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func credential(t *testing.T, token string) core.Credential {
	t.Helper()
	c, err := core.NewCredential(token)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func registered(t *testing.T, nick, token, listener string) Submission {
	return Submission{Author: core.Author{Nick: nick, Registered: true}, Credential: credential(t, token), Listener: listener}
}

func guest(nick, listener string) Submission {
	return Submission{Author: core.Author{Nick: nick}, Listener: listener}
}

func TestAuthorSubmissionValidation(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1000, 0)
	for _, sub := range []Submission{
		{Author: core.Author{Nick: "1bad"}},
		{Author: core.Author{Nick: "alice", Registered: true}},                              // registered needs a token
		{Author: core.Author{Nick: "alice"}, Credential: credential(t, authorToken)},        // a guest has none
		{Author: core.Author{Nick: "alice"}, Listener: strings.Repeat("l", 65)},             // listener is bounded
		{Author: core.Author{Nick: strings.Repeat("n", 31)}, Credential: core.Credential{}}, // AIRC nick length
	} {
		if _, _, err := s.AdmitAuthor(t.Context(), sub, request(), 0, now); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid submission %+v: %v", sub.Author, err)
		}
	}
	r := request()
	r.Operation = core.HistoryRead
	if _, _, err := s.AdmitAuthor(t.Context(), guest("alice", "http"), r, 0, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("read admitted to the delivery journal", err)
	}
}

func TestAuthorRequiresUpstream(t *testing.T) {
	c, _, _ := fixture(t)
	c.Upstream = nil
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.AdmitAuthor(t.Context(), guest("alice", "http"), request(), 0, time.Unix(1000, 0)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("author admitted without an upstream", err)
	}
}

// The same request ID from any listener or transport, with any nick spelling,
// is one job with one upstream identity; the first listener stays informational.
func TestAuthorReplayAcrossListenersIsOneJob(t *testing.T) {
	c, identity, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, now := t.Context(), time.Unix(1000, 0)
	first, created, err := s.AdmitAuthor(ctx, guest("Alice", "http-a"), request(), 0, now)
	if err != nil || !created {
		t.Fatal(err)
	}
	expected, _ := core.UpstreamID(c.Realm, "g:alice", "request")
	if first.Scope != "g:alice" || first.UpstreamID != expected || first.Author.Nick != "Alice" || first.Listener != "http-a" {
		t.Fatalf("guest job identity: %+v", first)
	}
	again, created, err := s.AdmitAuthor(ctx, guest("alice", "mqtt-b"), request(), 0, now.Add(time.Second))
	if err != nil || created || again.UpstreamID != first.UpstreamID || again.Listener != "http-a" || again.Author.Nick != "Alice" {
		t.Fatalf("cross-listener replay made another job: %+v created=%v err=%v", again, created, err)
	}
	if found, err := s.LookupAuthor(ctx, guest("ALICE", "udp-c"), "request"); err != nil || found.UpstreamID != first.UpstreamID {
		t.Fatal("cross-listener lookup", found, err)
	}
	changed := request()
	changed.Message = "different"
	if _, _, err := s.AdmitAuthor(ctx, guest("alice", "mqtt-b"), changed, 0, now); !errors.Is(err, ErrConflict) {
		t.Fatal("changed intent reused request ID", err)
	}
	// Registered and guest authors of one nickname are distinct owners, as in AIRC.
	reg, created, err := s.AdmitAuthor(ctx, registered(t, "alice", authorToken, "http-a"), request(), 0, now)
	if err != nil || !created || reg.Scope != "a:alice" || reg.UpstreamID == first.UpstreamID {
		t.Fatal("registered author collided with guest", reg, err)
	}
	again, created, err = s.AdmitAuthor(ctx, registered(t, "ALICE", authorToken, "ws-d"), request(), 0, now)
	if err != nil || created || again.UpstreamID != reg.UpstreamID {
		t.Fatal("registered cross-listener replay", again, created, err)
	}
	// Another author never sees these jobs.
	if _, err := s.LookupAuthor(ctx, identity, "request"); !errors.Is(err, ErrNotFound) {
		t.Fatal("another author read an author's job", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM jobs").Scan(&count); err != nil || count != 2 {
		t.Fatal("job count", count, err)
	}
	due, err := s.Ready(ctx, "", 2, now)
	if err != nil || len(due) != 2 || due[0].Lane != "a:alice" || due[1].Lane != "g:alice" {
		t.Fatal("author lanes not offered", due, err)
	}
	g, err := s.Reserve(ctx, "g:alice", "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil || !a.Credential.Empty() || a.Job.Author.Registered || a.Backend != *c.Upstream {
		t.Fatal("guest attempt", a, err)
	}
}

// journalBytes reads every SQLite file the store may write.
func journalBytes(t *testing.T, c config.Config) []byte {
	t.Helper()
	var all []byte
	for _, name := range []string{"journal.sqlite", "journal.sqlite-wal", "journal.sqlite-shm", "journal.sqlite-journal"} {
		data, err := os.ReadFile(filepath.Join(c.StateDir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		all = append(all, data...)
	}
	if len(all) == 0 {
		t.Fatal("no journal bytes")
	}
	return all
}

func TestAccountTokenNeverPersisted(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, now := t.Context(), time.Unix(1000, 0)
	sub := registered(t, "Writer", authorToken, "http")
	if _, _, err := s.AdmitAuthor(ctx, sub, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, "a:writer", "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil || a.Credential.Token() != authorToken {
		t.Fatal("attempt lost its in-memory credential", err)
	}
	if _, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, now); err != nil {
		t.Fatal(err)
	}
	var check string
	if err := s.db.QueryRowContext(ctx, "SELECT token_check FROM jobs").Scan(&check); err != nil || check != core.TokenCheck("writer", authorToken) {
		t.Fatal("token check not journalled", check, err)
	}
	inspect := func(stage string) {
		data := journalBytes(t, c)
		for _, secret := range [][]byte{[]byte(authorToken), []byte(strings.ToUpper(authorToken)), []byte(authorToken[:24]), []byte(authorToken[40:])} {
			if bytes.Contains(data, secret) {
				t.Fatalf("%s: journal contains account token material", stage)
			}
		}
		if !bytes.Contains(data, []byte(check)) {
			t.Fatalf("%s: inspected bytes do not include the journal (no token check found)", stage)
		}
	}
	inspect("open")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	inspect("closed")
}

func TestCredentialWaitAfterRestart(t *testing.T) {
	c, _, _ := fixture(t)
	ctx, now := t.Context(), time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitAuthor(ctx, registered(t, "writer", authorToken, "http"), request(), 0, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitAuthor(ctx, guest("visitor", "http"), request(), 0, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wrong := strings.Repeat("f", 64)
	if _, err := s.LookupAuthor(ctx, registered(t, "writer", wrong, "udp"), "request"); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("wrong token read a registered job", err)
	}
	j, err := s.LookupAuthor(ctx, registered(t, "writer", authorToken, "udp"), "request")
	if err != nil || j.Result.State != core.CredentialWait || j.Result.NextAction != "resubmit_credential" {
		t.Fatal("restart did not park the registered job", j.Result, err)
	}
	// The guest needs no credential and stays runnable.
	if g, err := s.LookupAuthor(ctx, guest("visitor", "udp"), "request"); err != nil || g.Result.State != core.Queued {
		t.Fatal("guest job parked", g.Result, err)
	}
	due, err := s.Ready(ctx, "", 4, now)
	if err != nil || len(due) != 1 || due[0].Scope != "g:visitor" {
		t.Fatal("parked job offered", due, err)
	}
	if _, err := s.Reserve(ctx, "a:writer", "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("parked job reserved without a credential", err)
	}
	// A wrong token is refused and changes nothing.
	if _, _, err := s.AdmitAuthor(ctx, registered(t, "writer", wrong, "ws"), request(), 0, now); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("wrong token resumed the job", err)
	}
	var state string
	if err := s.db.QueryRowContext(ctx, "SELECT state FROM jobs WHERE author_scope='a:writer'").Scan(&state); err != nil || state != "credential_wait" {
		t.Fatal("refused token changed the job", state, err)
	}
	// The matching token, on any listener, resumes the same job.
	j, created, err := s.AdmitAuthor(ctx, registered(t, "Writer", authorToken, "ws"), request(), 0, now)
	if err != nil || created || j.Result.State != core.Queued || j.Result.NextAction != "send" || j.Listener != "http" {
		t.Fatal("matching token did not resume", j.Result, created, err)
	}
	due, err = s.Ready(ctx, "", 4, now)
	if err != nil || len(due) != 2 {
		t.Fatal("resumed job not offered", due, err)
	}
	g, err := s.Reserve(ctx, "a:writer", "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil || a.Credential.Token() != authorToken || !a.Job.Author.Registered || a.Job.Author.Nick != "writer" {
		t.Fatal("resumed attempt", a.Job.Author, err)
	}
}

func TestCredentialWaitExpiresAndKeepsRecovery(t *testing.T) {
	c, _, _ := fixture(t)
	ctx, now := t.Context(), time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitAuthor(ctx, registered(t, "writer", authorToken, "http"), request(), 0, now); err != nil {
		t.Fatal(err)
	}
	second := request()
	second.RequestID = "in-flight"
	if _, _, err := s.AdmitAuthor(ctx, registered(t, "writer", authorToken, "http"), second, 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, "a:writer", "in-flight", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(now); err != nil {
		t.Fatal(err)
	}
	// Crash with an attempt in flight: restart recovers its uncertainty first,
	// then parks it for the credential its receipt lookup will need.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var state, resume string
	if err := s.db.QueryRowContext(ctx, "SELECT state,resume_state FROM jobs WHERE request_id='in-flight'").Scan(&state, &resume); err != nil || state != "credential_wait" || resume != "retry_wait" {
		t.Fatal("in-flight registered job", state, resume, err)
	}
	j, _, err := s.AdmitAuthor(ctx, registered(t, "writer", authorToken, "http"), second, 0, now)
	if err != nil || j.Result.State != core.RetryWait || !j.Result.AcceptanceUncertain || !j.Result.DuplicateRisk {
		t.Fatal("resume lost uncertainty", j.Result, err)
	}
	// Never resubmitted: the other job expires normally.
	expiry := time.Unix(1000+c.Retry.LifetimeSeconds, 0)
	if _, err := s.Maintain(ctx, expiry); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT state FROM jobs WHERE request_id='request'").Scan(&state); err != nil || state != "expired" {
		t.Fatal("parked job did not expire", state, err)
	}
	if _, held := s.secrets[secretKey("a:writer", "request")]; held {
		t.Fatal("expired job kept a token in memory")
	}
}

func TestAuthorKeyMigrationFromLegacyJournal(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "drained", true: "pending"}[pending], func(t *testing.T) {
			c, identity, _ := fixture(t)
			ctx := t.Context()
			path := filepath.Join(c.StateDir, "journal.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			if _, err := db.ExecContext(ctx, schema); err != nil {
				t.Fatal(err)
			}
			for _, migration := range journalMigrations[:authorKeyVersion-2] {
				if _, err := db.ExecContext(ctx, migration); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO metadata VALUES('configuration',?)", pinnedConfiguration(c)); err != nil {
				t.Fatal(err)
			}
			r := request()
			if err := r.Normalize(); err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(r)
			fingerprint, _ := r.Fingerprint()
			upstream, _ := legacyUpstreamID(c.Realm, "backend", "device", r.RequestID)
			terminal := "1000"
			state := "accepted"
			if pending {
				terminal, state = "NULL", "retry_wait"
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,terminal_at) VALUES (?,?,?,?,?,?,'retry',?,1000,1100,1160,604800,100,100,1,60,1000,1000,`+terminal+`)`, "device", "backend", r.RequestID, fingerprint, upstream, data, state); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO replay VALUES(?,?,?,?,1160)", "device", "backend", r.RequestID, fingerprint); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(ctx, c)
			if pending {
				if !errors.Is(err, ErrUndrainedJournal) || !strings.Contains(err.Error(), "drain") {
					t.Fatal("pending legacy journal migrated", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var version, jobs, replay int
			if err := s.db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM jobs),(SELECT count(*) FROM replay)").Scan(&jobs, &replay); err != nil || jobs != 0 || replay != 0 {
				t.Fatal("terminal legacy rows kept", jobs, replay, err)
			}
			if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion || schemaVersion != authorKeyVersion {
				t.Fatal("version", version, err)
			}
			checkAuthorKeyedJournal(t, s, c, identity)
		})
	}
}

// A request decoded as any carrier receives it never leaves its account
// token or access password in the journal; its source stays in memory and
// reaches the attempt, and the account ID AIRC reports is recorded.
func TestDecodedSubmissionKeepsSecretsOutOfTheJournal(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, now := t.Context(), time.Unix(1000, 0)
	const password = "open-sesame-listener-password"
	decoded, err := core.DecodeSubmission([]byte(`{"version":1,"request_id":"request","operation":"send","target":{"kind":"room","name":"#gateway"},"message":"hello","author":{"nick":"Writer","token":"` + authorToken + `"},"password":"` + password + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	sub := Submission{Author: *decoded.Author, Credential: decoded.Credential, Listener: "http", Source: "203.0.113.9"}
	if _, _, err := s.AdmitAuthor(ctx, sub, decoded.Request, 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, "a:writer", "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil || a.Source != "203.0.113.9" || a.Credential.Token() != authorToken {
		t.Fatal("attempt lost its source or credential", a.Source, err)
	}
	const accountID = "0123456789abcdef0123456789abcdef"
	if err := g.NoteAccount("not-an-id"); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid account ID accepted", err)
	}
	if err := g.NoteAccount(accountID); err != nil {
		t.Fatal(err)
	}
	// The account ID is written by the outcome's own transaction.
	if _, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, now); err != nil {
		t.Fatal(err)
	}
	j, err := s.LookupAuthor(ctx, sub, "request")
	if err != nil || j.AccountID != accountID {
		t.Fatal("account ID not recorded", j.AccountID, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data := journalBytes(t, c)
	for _, secret := range []string{authorToken, password, "203.0.113.9"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("journal contains %q", secret)
		}
	}
}

// A journal of the removed identity model is refused, not run: its pinned
// configuration named principals, or it still owes work for one.
func TestRemovedIdentityJournalIsRefused(t *testing.T) {
	c, _, _ := fixture(t)
	ctx := t.Context()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	r := request()
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(r)
	fingerprint, _ := r.Fingerprint()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO jobs (author_scope,request_id,author_nick,registered,principal,binding,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at) VALUES ('a:author',?,'author',1,'device','backend',?,'legacy-upstream',?,'retry','queued',1000,1100,0,604800,100,100,1,60,1000,1000)`, r.RequestID, fingerprint, data); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(ctx, c); !errors.Is(err, ErrUndrainedJournal) {
		if err == nil {
			s.Close()
		}
		t.Fatal("unfinished job of a removed principal reopened", err)
	}

	c, _, _ = fixture(t)
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE metadata SET value='{"Realm":"fixture","Bindings":[{"ID":"b","Network":"unix","Address":"/s","Account":"a","TLSServerName":""}],"Principals":[{"ID":"p","Binding":"b"}]}' WHERE key='configuration'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(ctx, c); !errors.Is(err, ErrPinnedConfiguration) {
		if err == nil {
			s.Close()
		}
		t.Fatal("journal pinned to removed principals reopened", err)
	}
}

// The account ID a registered author's attempt logged in as is saved in the
// acceptance's own transaction; a guest's attempt records none, and an
// unconsumed grant takes none.
func TestAccountIDSavedWithAcceptance(t *testing.T) {
	c, _, _ := fixture(t)
	ctx, now := t.Context(), time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const accountID = "0123456789abcdef0123456789abcdef"
	for _, sub := range []Submission{registered(t, "author", authorToken, "http"), guest("author", "http")} {
		if _, _, err := s.AdmitAuthor(ctx, sub, request(), 0, now); err != nil {
			t.Fatal(err)
		}
		g, err := s.Reserve(ctx, sub.Author.Scope(), "request", SendAttempt, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.NoteAccount(accountID); !errors.Is(err, ErrNotReady) {
			t.Fatal("unconsumed grant took an account ID", err)
		}
		a, err := g.Consume(now)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.NoteAccount(accountID); err != nil {
			t.Fatal(err)
		}
		j, err := s.RecordAcceptance(ctx, g, receiptFor(a), now, now)
		if err != nil || j.Result.State != core.Accepted {
			t.Fatal("acceptance not saved", j.Result.State, err)
		}
		want := ""
		if sub.Author.Registered {
			want = accountID
		}
		if j, err := s.LookupAuthor(ctx, sub, "request"); err != nil || j.AccountID != want {
			t.Fatalf("%s: account ID %q, want %q (%v)", sub.Author.Scope(), j.AccountID, want, err)
		}
	}
}
