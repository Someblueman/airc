//go:build darwin || linux

package gateway_test

// The route suite runs the author scenarios that apply where the route, not
// the request, decides who AIRC posts as: TLS syslog, RFC 5848 signed syslog
// and SNMPv3 traps post as the route's bound author, and the IRC bridge posts
// a services account as its guest or its mapped account. The cross-carrier
// suite's model (a client sends contract requests naming their author) does
// not fit them: their peers send provider records whose request IDs the
// gateway derives, and for IRC the gateway is the client of a scripted
// server. Each carrier gets its own current-tree aircd and in-process gateway
// with one route posting to #route and an HTTP listener the suite reads
// receipts from; an independent IRC client observes what AIRC posts, aircd's
// history is the count of record, and the journal is read directly. Failures
// read TestRouteSuite/<carrier>/<scenario>.
//
// Adding a carrier: register a routeCarrier whose provider configures the
// route and speaks its peer. Provider verification no scenario covers goes in
// the carrier's wire hook; behaviour that needs its own gateway configuration
// keeps a focused test. Contract-JSON route ingress (Redis, PostgreSQL, SFTP,
// SCP) runs the same author scenarios in the adapter's TestCarrierAdmission.

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// routeCarrier is one row of the route suite.
type routeCarrier struct {
	name string
	// guest, when set, is the guest AIRC posts the peer's messages as;
	// otherwise the route's bound author is the registered alice.
	guest string
	// singleLine: the carrier cannot carry a line break in one message.
	singleLine bool
	// provider configures the carrier's route, posting to #route as the
	// route author, and returns its peer side.
	provider func(t *testing.T, e *routeEnv, c *config.Config) routeProvider
	// refusals name inputs the gateway must refuse (routeProvider.refuse).
	refusals []string
	// wire, if set, runs provider-specific checks as the "wire" subtest.
	wire func(t *testing.T, e *routeEnv)
}

// routeProvider is the peer side of one carrier.
type routeProvider interface {
	// connect opens a peer on the running gateway.
	connect(t *testing.T) routePeer
	// refuse sends one refused input. It returns the request ID the input
	// would have had ("" when it has none) and whether the gateway has
	// visibly acted on it (closed the connection) before returning.
	refuse(t *testing.T, kind string) (id string, settled bool)
	// partial leaves an incomplete input open, or does nothing.
	partial(t *testing.T)
}

// routePeer is one connection of a carrier's peer.
type routePeer interface {
	// deliver sends a new message carrying text.
	deliver(t *testing.T, text string) routeItem
	// replay sends a delivered message again.
	replay(t *testing.T, item routeItem)
	close()
}

// routeItem is one delivered message.
type routeItem struct {
	id   string   // the request ID the gateway derives
	text string   // what AIRC posts
	wire [][]byte // what the peer sent
}

var routeCarriers = []routeCarrier{
	{name: "syslog_tls", provider: newSyslogRoute, refusals: syslogRefusals},
	{name: "syslog_signed", provider: newSignedSyslogRoute, refusals: signedSyslogRefusals, wire: signedSyslogRouteWire},
	{name: "snmp_v3", provider: newSNMPRoute, refusals: snmpRefusals, wire: snmpRouteWire},
	{name: "irc", guest: "irc-alice", singleLine: true, provider: newIRCRoute, refusals: ircRefusals, wire: ircRouteWire},
}

// routeScenarios run in this order; shutdown stops the gateway, so it is last.
var routeScenarios = []struct {
	name    string
	applies func(routeCarrier) bool
	run     func(*testing.T, *routeEnv)
}{
	{"route_post", nil, routePost},
	{"replay_after_reconnect", nil, routeReplayAfterReconnect},
	{"refusals_leave_no_job", nil, routeRefusals},
	{"wire", func(c routeCarrier) bool { return c.wire != nil }, func(t *testing.T, e *routeEnv) { e.carrier.wire(t, e) }},
	{"shutdown_joins", nil, routeShutdownJoins},
}

func TestRouteSuite(t *testing.T) {
	for _, c := range routeCarriers {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newRouteEnv(t, c)
			for _, s := range routeScenarios {
				if s.applies == nil || s.applies(c) {
					t.Run(s.name, func(t *testing.T) { s.run(t, e) })
				}
			}
		})
	}
}

// routeRoom is the one room every route posts to.
const routeRoom = "#route"

var routeTarget = core.Target{Kind: "room", Name: routeRoom}

// routeEnv is one carrier's aircd and gateway.
type routeEnv struct {
	carrier           routeCarrier
	socket, accountID string
	config            config.Config
	gateway           *authorGateway
	// address is the route listener's ("" for a bridge); receipts is the
	// HTTP receipts listener's base URL.
	address, receipts string
	// nick and token are who AIRC posts the route's messages as.
	nick, token string
	provider    routeProvider
	observed    *suiteObserver
	// starts counts gateway starts, so bridge peers know to wait for the
	// gateway to reconnect.
	starts   int
	sequence int
}

// newRouteEnv starts aircd and a gateway serving c's route; edit, if set,
// changes the gateway configuration first.
func newRouteEnv(t *testing.T, c routeCarrier, edit ...func(*config.Config)) *routeEnv {
	t.Helper()
	socket, gatewayToken := currentNative(t)
	provision := dial(t, socket, "alice", aliceToken, true, true)
	accountID := provision.AccountID()
	_ = provision.Close()
	watcher := dial(t, socket, "watcher", "", false, false)
	if err := watcher.Join(routeRoom); err != nil {
		t.Fatal(err)
	}
	next(t, watcher, func(e *irc.JoinEvent) bool { return e.Agent == "watcher" && e.Channel == routeRoom })
	cfg := authorConfig(t, socket, gatewayToken, config.Listener{ID: "receipts", Protocol: "http", Limits: fixtureListenerLimits()})
	e := &routeEnv{carrier: c, socket: socket, accountID: accountID, nick: "alice", token: aliceToken, observed: observe(watcher)}
	if c.guest != "" {
		e.nick, e.token = c.guest, ""
	}
	e.provider = c.provider(t, e, &cfg)
	for _, f := range edit {
		f(&cfg)
	}
	e.config = cfg
	e.start(t)
	return e
}

// start starts (or restarts) the gateway on the environment's journal.
func (e *routeEnv) start(t *testing.T) {
	t.Helper()
	e.gateway = startAuthorGateway(t, e.config)
	e.address, e.receipts = e.gateway.addresses["route"], "http://"+e.gateway.addresses["receipts"]
	e.starts++
}

// routeListener is the route's listener with the suite's ID.
func routeListener(protocol string) config.Listener {
	return config.Listener{ID: "route", Protocol: protocol, Operations: []core.Operation{core.Send}}
}

// aliceTokenFile writes alice's token to a private file of its own, as each
// route's credential file must be.
func (e *routeEnv) aliceTokenFile(t *testing.T, c *config.Config, name string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(c.StateDir), name+".token")
	if err := os.WriteFile(path, []byte(aliceToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// text is a whole Unicode message body naming the carrier, on two lines where
// the carrier carries them.
func (e *routeEnv) text(name string) string {
	e.sequence++
	if e.carrier.singleLine {
		return fmt.Sprintf("%s %d via %s whole body café 世界 🙂  ", name, e.sequence, e.carrier.name)
	}
	return fmt.Sprintf("%s %d via %s\nwhole body café 世界 🙂", name, e.sequence, e.carrier.name)
}

// posted waits for AIRC to post item as the route author and for its receipt
// to report that message, sent once.
func (e *routeEnv) posted(t *testing.T, item routeItem) *irc.MessageEvent {
	t.Helper()
	seen := e.observed.wait(t, item.text)
	if seen.From != e.nick || seen.Target != routeRoom {
		t.Fatalf("AIRC posted %+v, want %s in %s", seen, e.nick, routeRoom)
	}
	result := waitReceipt(t, e.receipts, item.id, authorHeaders(e.nick, e.token))
	if result.State != core.Accepted || !result.Durable || result.MessageID != seen.ID || result.SendAttempts != 1 {
		t.Fatalf("receipt %+v %+v for %+v", result, result.Failure, seen)
	}
	return seen
}

// messages is what aircd holds in the route's room.
func (e *routeEnv) messages(t *testing.T) []*irc.MessageMetadata {
	t.Helper()
	return nativeWebTransportMessages(t, e.socket, routeRoom)
}

// count is how many messages with text aircd holds.
func (e *routeEnv) count(t *testing.T, text string) int {
	t.Helper()
	n := 0
	for _, m := range e.messages(t) {
		if m.Message == text {
			n++
		}
	}
	return n
}

// submission is the journal key of the route author.
func (e *routeEnv) submission(t *testing.T) store.Submission {
	t.Helper()
	if e.token == "" {
		return store.Submission{Author: core.Author{Nick: e.nick}}
	}
	return registeredSubmission(t, e.nick, e.token)
}

// routeJournalQueries read every journal row a route input can create.
var routeJournalQueries = []string{
	"SELECT author_scope, request_id, state FROM jobs",
	"SELECT * FROM replay",
	"SELECT * FROM signed_syslog_state",
	"SELECT * FROM snmp_engines",
	"SELECT * FROM snmp_owners",
	"SELECT * FROM snmp_seen",
}

// journal renders those rows, sorted, read without disturbing the gateway.
func (e *routeEnv) journal(t *testing.T) string {
	t.Helper()
	db := e.openJournal(t)
	defer db.Close()
	var rows []string
	for _, query := range routeJournalQueries {
		result, err := db.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(query, err)
		}
		columns, err := result.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for result.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := result.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, fmt.Sprintf("%s: %v", query, values))
		}
		if err := result.Close(); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(rows)
	return strings.Join(rows, "\n")
}

// noJob checks that the journal holds no job for request id.
func (e *routeEnv) noJob(t *testing.T, id string) {
	t.Helper()
	db := e.openJournal(t)
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("refused input %s reached intake: %d %v", id, count, err)
	}
}

func (e *routeEnv) openJournal(t *testing.T) *sql.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(e.config.StateDir, "journal.sqlite"), RawQuery: "mode=ro&_pragma=busy_timeout(3000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// The route posts the peer's message, exactly, as the route author.
func routePost(t *testing.T, e *routeEnv) {
	peer := e.provider.connect(t)
	defer peer.close()
	e.posted(t, peer.deliver(t, e.text("post")))
}

// A message delivered again, on a new connection where the carrier has them,
// is the same job: AIRC holds it once.
func routeReplayAfterReconnect(t *testing.T, e *routeEnv) {
	peer := e.provider.connect(t)
	item := peer.deliver(t, e.text("replayed"))
	seen := e.posted(t, item)
	peer.close()
	peer = e.provider.connect(t)
	defer peer.close()
	peer.replay(t, item)
	// A later message fences the replay without an acknowledgement.
	e.posted(t, peer.deliver(t, e.text("replay fence")))
	if result := waitReceipt(t, e.receipts, item.id, authorHeaders(e.nick, e.token)); result.MessageID != seen.ID || result.SendAttempts != 1 {
		t.Fatalf("replay changed the job: %+v", result)
	}
	if n := e.count(t, item.text); n != 1 {
		t.Fatalf("AIRC holds %d messages for one delivery", n)
	}
}

// Every refused input leaves the journal and the room untouched, and the
// route keeps posting afterwards.
func routeRefusals(t *testing.T, e *routeEnv) {
	before, held := e.journal(t), len(e.messages(t))
	var ids []string
	for _, kind := range e.carrier.refusals {
		t.Run(kind, func(t *testing.T) {
			id, settled := e.provider.refuse(t, kind)
			if settled {
				if after := e.journal(t); after != before {
					t.Fatalf("refusal changed the journal:\n%s\nwas:\n%s", after, before)
				}
			}
			if id != "" {
				ids = append(ids, id)
			}
		})
	}
	peer := e.provider.connect(t)
	defer peer.close()
	e.posted(t, peer.deliver(t, e.text("refusal fence")))
	for _, id := range ids {
		e.noJob(t, id)
	}
	if messages := e.messages(t); len(messages) != held+1 {
		t.Fatal("a refused input posted", messages[held:])
	}
}

// The gateway stops promptly with a peer connected (mid-record where the
// carrier frames records), releases its journal, and the journal keeps the
// accepted job; a restarted gateway treats the same message as a replay.
func routeShutdownJoins(t *testing.T, e *routeEnv) {
	peer := e.provider.connect(t)
	item := peer.deliver(t, e.text("shutdown"))
	seen := e.posted(t, item)
	before := e.journal(t)
	e.provider.partial(t)
	e.gateway.stop(t)
	peer.close()
	if t.Failed() {
		return
	}
	s, err := store.Open(t.Context(), e.config)
	if err != nil {
		t.Fatal("journal not released:", err)
	}
	job, err := s.LookupAuthor(t.Context(), e.submission(t), item.id)
	if err != nil || job.Result.State != core.Accepted || job.Result.MessageID != seen.ID || job.Result.SendAttempts != 1 || job.Request.Message != item.text || e.token != "" && job.AccountID != e.accountID {
		t.Fatalf("journaled job %+v (account %q): %v", job.Result, job.AccountID, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if after := e.journal(t); after != before {
		t.Fatalf("input in flight at shutdown changed the journal:\n%s\nwas:\n%s", after, before)
	}
	e.start(t)
	peer = e.provider.connect(t)
	defer peer.close()
	peer.replay(t, item)
	e.posted(t, peer.deliver(t, e.text("restart fence")))
	e.gateway.stop(t)
	if n := e.count(t, item.text); n != 1 {
		t.Fatalf("AIRC holds %d messages after a restart", n)
	}
}

// jobState is the journaled state of request id ("" when there is none).
func (e *routeEnv) jobState(t *testing.T, id string) core.State {
	t.Helper()
	db := e.openJournal(t)
	defer db.Close()
	var state string
	if err := db.QueryRowContext(t.Context(), "SELECT state FROM jobs WHERE request_id=?", id).Scan(&state); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return core.State(state)
}
