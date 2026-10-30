//go:build darwin || linux

package gateway_test

// The cross-carrier suite runs one set of author scenarios against every
// carrier in suiteCarriers, so a behaviour the contract promises on every
// carrier is written once. Each carrier gets its own current-tree aircd and
// an in-process gateway (startAuthorGateway) whose listeners admit the
// scenario rooms: the carrier's plaintext listener, a twin that requires the
// listener password, a TLS twin, and an HTTP listener the suite reads
// receipts from. An independent IRC client in the scenario rooms observes
// what AIRC posts, aircd's own history (CHECK) is the count of record, and
// the journal is read directly to prove a refusal never reached intake.
// Failures read TestCarrierSuite/<carrier>/<scenario>.
//
// Adding a carrier: register one suiteCarrier (append it here, or call
// registerSuiteCarriers from a package-level var in the carrier family's own
// file) with its listener, a dial
// function returning a minimal independent client (a suiteClient, and a
// suiteWatcher for streamed watches) and the flags saying which scenarios
// apply. Wire behaviour no scenario covers (framing, acknowledgement frames,
// refusal frames, protocol options) goes in the entry's wire hook, which runs
// against the same listeners, in a carrier_suite_<carrier>_test.go file;
// behaviour that needs its own gateway configuration or process keeps a
// focused test in the carrier's own files.

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/irc"
)

// suiteCarrier is one row of the suite.
type suiteCarrier struct {
	name string
	// listener is the carrier's plaintext listener; the suite sets its ID,
	// address, password and limits.
	listener config.Listener
	// dial connects a client; a connect-time refusal is returned as an error.
	dial func(*testing.T, suiteDial) (suiteClient, error)
	// connectPassword: the password is checked once per connection (MQTT
	// CONNECT), not per request.
	connectPassword bool
	// results: replies carry the operation's result. MQTT only acknowledges
	// intake; the suite reads its outcomes from receipts.
	results bool
	reads   bool // history.read, search and context.read
	tls     bool // the listener also serves TLS
	// secure: the carrier is encrypted by definition (QUIC). Every listener
	// serves the suite's P-256 certificate and every client's suiteDial.tls
	// trusts it.
	secure bool
	// alpn is the protocol a TLS client negotiates ("" for none).
	alpn string
	// watchModes, when set, are the stream modes capabilities.get reports.
	watchModes []string
	session    bool // account.identify sets the connection's author
	watch      bool // the client implements suiteWatcher
	// wire, if set, runs carrier-specific checks against the suite's
	// listener as the "wire" subtest.
	wire func(t *testing.T, e *suiteEnv)
	// configure, if set, adjusts each of the carrier's listeners before the
	// gateway starts (a free datagram port); it may skip
	// the carrier on a host that lacks the feature.
	configure func(t *testing.T, l *config.Listener)
}

// suiteDial is where and how a client connects. password is the listener
// access password the client presents when it connects (connectPassword
// carriers); other carriers carry it in each request body. tls, when set,
// is the client's TLS configuration.
type suiteDial struct {
	address, password string
	tls               *tls.Config
}

// suiteClient is a minimal independent client of one carrier.
type suiteClient interface {
	// call sends one contract-v1 JSON request (which need not be valid) and
	// reads its outcome.
	call(t *testing.T, request []byte) suiteReply
	// send writes a request without reading its outcome.
	send(t *testing.T, request []byte)
	close()
}

// suiteWatcher streams a watch on a client's connection. pages returns the
// next page, or nil once the stream ends; cancel abandons the watch.
type suiteWatcher interface {
	watch(t *testing.T, request []byte) (pages func() *backend.HistoryPage, cancel func())
}

// suiteReply is a carrier's answer to one request.
type suiteReply struct {
	result  *dispatch.DetachedResult // the operation's result
	acked   bool                     // intake acknowledged without a result (MQTT PUBACK)
	failure *core.Failure            // a correlated refusal
	closed  bool                     // the carrier closed the connection instead
}

func (r suiteReply) String() string {
	return fmt.Sprintf("result=%+v acked=%t failure=%+v closed=%t", r.result, r.acked, r.failure, r.closed)
}

var suiteCarriers = []suiteCarrier{
	{name: "http", listener: config.Listener{Protocol: "http"}, dial: dialHTTPSuite, results: true, tls: true, reads: true, wire: httpSuiteWire},
	{name: "websocket", listener: config.Listener{Protocol: "websocket"}, dial: dialWebSocketSuite, results: true, tls: true, reads: true, session: true, watch: true, watchModes: []string{"long_poll", "websocket"}},
	{name: "framed", listener: config.Listener{Protocol: "framed"}, dial: dialFramedSuite("tcp"), results: true, tls: true, alpn: adapter.FramedALPN, reads: true, session: true, watch: true, watchModes: []string{"long_poll", "framed_stream"}},
	{name: "unix", listener: config.Listener{Protocol: "unix"}, dial: dialFramedSuite("unix"), results: true, reads: true, session: true, watch: true, watchModes: []string{"long_poll", "framed_stream"}, wire: unixSuiteWire},
	{name: "grpc", listener: config.Listener{Protocol: "grpc"}, dial: dialGRPCSuite, results: true, tls: true, alpn: "h2", reads: true, watch: true},
	{name: "mqtt", listener: config.Listener{Protocol: "mqtt", MQTT: &config.MQTT{Routes: []config.MQTTRoute{{Topic: suiteMQTTTopic}}}}, dial: dialMQTTSuite, connectPassword: true, tls: true, session: true, wire: mqttSuiteWire(mqttConnect)},
}

// registerSuiteCarriers adds carriers defined in a carrier family's own file,
// so families can join the suite without editing this table:
//
//	var _ = registerSuiteCarriers(suiteCarrier{name: "quic", ...})
func registerSuiteCarriers(carriers ...suiteCarrier) bool {
	suiteCarriers = append(suiteCarriers, carriers...)
	return true
}

const suitePassword = "suite listener password"

// suiteScenarios run in this order; shutdown stops the gateway, so it is last.
var suiteScenarios = []struct {
	name    string
	applies func(suiteCarrier) bool
	run     func(*testing.T, *suiteEnv)
}{
	{"guest_post", nil, suiteGuestPost},
	{"reply_after_commit", nil, suiteReplyAfterCommit},
	{"registered_post", nil, suiteRegisteredPost},
	{"author_refusals", nil, suiteAuthorRefusals},
	{"malformed_refused", nil, suiteMalformedRefused},
	{"target_refused", nil, suiteTargetRefused},
	{"conflicting_retry_refused", nil, suiteConflictingRetry},
	{"retry_after_lost_reply", nil, suiteRetryAfterLostReply},
	{"retry_on_another_carrier", nil, suiteRetryOnAnotherCarrier},
	{"bounded_reads", func(c suiteCarrier) bool { return c.reads }, suiteBoundedReads},
	{"listener_password", nil, suiteListenerPassword},
	{"tls_post", func(c suiteCarrier) bool { return c.tls }, suiteTLSPost},
	{"tls_refusals", func(c suiteCarrier) bool { return c.tls }, suiteTLSRefusals},
	{"identify_then_post", func(c suiteCarrier) bool { return c.session }, suiteIdentifyThenPost},
	{"watch_then_cancel", func(c suiteCarrier) bool { return c.watch }, suiteWatchThenCancel},
	{"wire", func(c suiteCarrier) bool { return c.wire != nil }, func(t *testing.T, e *suiteEnv) { e.carrier.wire(t, e) }},
	{"shutdown_joins", nil, suiteShutdownJoins},
}

func TestCarrierSuite(t *testing.T) {
	t.Parallel()
	for _, c := range suiteCarriers {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newSuiteEnv(t, c)
			for _, s := range suiteScenarios {
				if s.applies == nil || s.applies(c) {
					t.Run(s.name, func(t *testing.T) { s.run(t, e) })
				}
			}
		})
	}
}

// suiteEnv is one carrier's aircd and gateway. The carrier's listeners
// admit exactly the scenario rooms.
type suiteEnv struct {
	carrier           suiteCarrier
	socket, accountID string
	config            config.Config
	gateway           *authorGateway
	address, guarded  string // the carrier's listener and its password twin
	secure            string // its TLS twin
	cert              testtls.Certificate
	receipts          string // base URL of the HTTP receipts listener
	observed          *suiteObserver
	statusClient      *irc.Client
	sequence          int
}

// suiteRooms are the scenario rooms the observer joins.
var suiteRooms = []string{"#guest", "#commit", "#tls", "#registered", "#refusals", "#malformed", "#conflict", "#retry", "#cross", "#reads", "#password", "#identify", "#watch", "#wire", "#shutdown"}

func newSuiteEnv(t *testing.T, c suiteCarrier) *suiteEnv {
	t.Helper()
	socket, gatewayToken := currentNative(t)
	provision := dial(t, socket, "alice", aliceToken, true, true)
	accountID := provision.AccountID()
	_ = provision.Close()
	if len(accountID) != 32 {
		t.Fatal("provisioning did not report an account ID", accountID)
	}
	watcher := dialObserver(t, socket)
	for _, room := range suiteRooms {
		if err := watcher.Join(room); err != nil {
			t.Fatal(err)
		}
		next(t, watcher, func(e *irc.JoinEvent) bool { return e.Agent == "watcher" && e.Channel == room })
	}
	var rooms []core.Target
	for _, room := range suiteRooms {
		rooms = append(rooms, core.Target{Kind: "room", Name: room})
	}
	var cert testtls.Certificate
	if c.secure {
		cert = testtls.NewP256(t)
	} else if c.tls {
		cert = testtls.New(t)
	}
	listener := func(id, password string) config.Listener {
		l := c.listener
		l.ID, l.PasswordFile, l.Limits, l.Targets = id, password, fixtureListenerLimits(), rooms
		if c.secure {
			l.TLSCertFile, l.TLSKeyFile = cert.CertPath, cert.KeyPath
		}
		if c.configure != nil {
			c.configure(t, &l)
		}
		return l
	}
	listeners := []config.Listener{listener("carrier", ""), listener("guarded", suitePassword), {ID: "receipts", Protocol: "http", Limits: fixtureListenerLimits()}}
	if c.tls {
		secure := listener("tls", "")
		secure.TLSCertFile, secure.TLSKeyFile = cert.CertPath, cert.KeyPath
		listeners = append(listeners, secure)
	}
	cfg := authorConfig(t, socket, gatewayToken, listeners...)
	g := startAuthorGateway(t, cfg)
	e := &suiteEnv{carrier: c, socket: socket, accountID: accountID, config: cfg, gateway: g, address: g.addresses["carrier"], guarded: g.addresses["guarded"], secure: g.addresses["tls"], cert: cert, receipts: "http://" + g.addresses["receipts"], observed: observe(watcher)}
	e.statusClient = nativeStatusClient(t, socket)
	return e
}

// dial connects a client to the carrier's open listener.
func (e *suiteEnv) dial(t *testing.T) suiteClient {
	t.Helper()
	return e.dialWith(t, suiteDial{address: e.address})
}

func (e *suiteEnv) dialWith(t *testing.T, d suiteDial) suiteClient {
	t.Helper()
	client, err := e.connect(t, d)
	if err != nil {
		t.Fatalf("%s: connect: %v", e.carrier.name, err)
	}
	t.Cleanup(client.close)
	return client
}

// connect dials a client; a secure carrier's client trusts the suite's
// certificate.
func (e *suiteEnv) connect(t *testing.T, d suiteDial) (suiteClient, error) {
	if e.carrier.secure && d.tls == nil {
		d.tls = e.cert.Client.Clone()
	}
	return e.carrier.dial(t, d)
}

// id is a request ID unique to this carrier's environment.
func (e *suiteEnv) id(name string) string {
	e.sequence++
	return fmt.Sprintf("%s-%s-%d", e.carrier.name, name, e.sequence)
}

// guest is a guest nickname for this carrier.
func (e *suiteEnv) guest(name string) string { return e.carrier.name + "-" + name }

// text is a whole multi-line Unicode message body naming the carrier.
func (e *suiteEnv) text(name string) string {
	return fmt.Sprintf("%s via %s\nwhole body café 🙂", name, e.carrier.name)
}

func sendBody(t *testing.T, id, room, text, nick, token string) []byte {
	t.Helper()
	return mustBody(t, sendRequest(id, room, text, nick, token))
}

// admitted checks that a reply admits request id.
func (e *suiteEnv) admitted(t *testing.T, r suiteReply, id string) {
	t.Helper()
	if !e.carrier.results {
		if !r.acked {
			t.Fatalf("%s not acknowledged: %v", id, r)
		}
		return
	}
	if r.result == nil || r.result.Result == nil || r.result.Result.RequestID != id || !r.result.Result.Durable {
		t.Fatalf("%s not admitted: %v", id, r)
	}
}

// refused checks that a reply refuses a request, with code when the carrier
// answers with a failure.
func refused(t *testing.T, r suiteReply, code core.ErrorCode) {
	t.Helper()
	t.Logf("refused: %v", r)
	if r.closed {
		return
	}
	if r.failure == nil || r.failure.Code != code {
		t.Fatalf("want a %s refusal: %v", code, r)
	}
}

// receipt waits for the job's terminal state, read over HTTP as the author.
func (e *suiteEnv) receipt(t *testing.T, id, nick, token string) core.Result {
	t.Helper()
	return waitReceipt(t, e.receipts, id, authorHeaders(nick, token))
}

// posted waits for the job to be accepted and checks what AIRC posted.
func (e *suiteEnv) posted(t *testing.T, id, room, text, nick, token string) *irc.MessageEvent {
	t.Helper()
	result := e.receipt(t, id, nick, token)
	if result.State != core.Accepted || result.MessageID == "" || result.SendAttempts != 1 {
		t.Fatalf("%s: %+v %+v", id, result, result.Failure)
	}
	seen := e.observed.wait(t, text)
	if seen.ID != result.MessageID || seen.From != nick || seen.Target != room || seen.Message != text {
		t.Fatalf("%s: AIRC posted %+v, receipt %+v", id, seen, result)
	}
	return seen
}

// nativeCount is how many messages with text aircd holds in room.
func (e *suiteEnv) nativeCount(t *testing.T, room, text string) int {
	t.Helper()
	count := 0
	for _, m := range nativeWebTransportMessages(t, e.socket, room) {
		if m.Message == text {
			count++
		}
	}
	return count
}

// noJob checks that the journal holds no job for id.
func (e *suiteEnv) noJob(t *testing.T, id string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(e.config.StateDir, "journal.sqlite"), RawQuery: "mode=ro&_pragma=busy_timeout(3000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("refused request %s reached intake: %d %v", id, count, err)
	}
}

// suiteObserver records every message an independent IRC client sees.
type suiteObserver struct {
	mu       sync.Mutex
	messages []*irc.MessageEvent
	// disconnected: the client's events ended, so later scenarios cannot
	// be observed.
	disconnected bool
}

// dialObserver connects the suite's independent IRC observer. aircd sends
// no keepalive, so its read timeout must outlast the quietest scenario (a
// carrier waiting out its own deadline); the default ten seconds ended the
// observer and failed every later scenario.
func dialObserver(t *testing.T, socket string) *irc.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: socket, Nick: "watcher", ReadTimeout: 120 * time.Second, WriteTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func observe(client *irc.Client) *suiteObserver {
	o := &suiteObserver{}
	go func() {
		for event := range client.Events() {
			if m, ok := event.(*irc.MessageEvent); ok {
				o.mu.Lock()
				o.messages = append(o.messages, m)
				o.mu.Unlock()
			}
		}
		o.mu.Lock()
		o.disconnected = true
		o.mu.Unlock()
	}()
	return o
}

// wait returns the first observed message with text.
func (o *suiteObserver) wait(t *testing.T, text string) *irc.MessageEvent {
	t.Helper()
	var found *irc.MessageEvent
	if !testwait.Poll(10*time.Second, 20*time.Millisecond, func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		for _, m := range o.messages {
			if m.Message == text {
				found = m
				return true
			}
		}
		if o.disconnected {
			t.Fatalf("the IRC observer disconnected before AIRC posted %q", text)
		}
		return false
	}) {
		t.Fatalf("AIRC never posted %q", text)
	}
	return found
}

// A guest names only a nickname: AIRC posts as that guest. The body is
// large enough to span several frames on carriers that fragment.
func suiteGuestPost(t *testing.T, e *suiteEnv) {
	id, nick, text := e.id("guest"), e.guest("guest"), e.text("guest")+"\n"+strings.Repeat("世界 🙂", 200)
	e.admitted(t, e.dial(t).call(t, sendBody(t, id, "#guest", text, nick, "")), id)
	e.posted(t, id, "#guest", text, nick, "")
}

// No reply (a result, a PUBACK) precedes committed intake: while another writer holds the journal the
// carrier stays silent, and its reply follows the commit of exactly one job.
func suiteReplyAfterCommit(t *testing.T, e *suiteEnv) {
	client := e.dial(t)
	db, err := sql.Open("sqlite", filepath.Join(e.config.StateDir, "journal.sqlite")+"?_pragma=busy_timeout(1000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lock, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	released := make(chan time.Time, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		at := time.Now()
		if _, err := lock.ExecContext(context.Background(), "ROLLBACK"); err != nil {
			at = time.Time{}
		}
		released <- at
	}()
	id, nick, text := e.id("commit"), e.guest("commit"), e.text("commit")
	r := client.call(t, sendBody(t, id, "#commit", text, nick, ""))
	replied := time.Now()
	if at := <-released; at.IsZero() || replied.Before(at) {
		t.Fatalf("reply at %v preceded the commit at %v", replied, at)
	}
	e.admitted(t, r, id)
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", id).Scan(&count); err != nil || count != 1 {
		t.Fatal("reply without one committed job", count, err)
	}
	e.posted(t, id, "#commit", text, nick, "")
}

// A registered author names its token: AIRC posts as the account.
func suiteRegisteredPost(t *testing.T, e *suiteEnv) {
	id, text := e.id("registered"), e.text("registered")
	e.admitted(t, e.dial(t).call(t, sendBody(t, id, "#registered", text, "alice", aliceToken)), id)
	e.posted(t, id, "#registered", text, "alice", aliceToken)
}

// AIRC decides who may post: a guest on a registered nickname, a guest on a
// nickname a live client holds and a wrong token are each admitted and then
// blocked with their own permanent failure; nothing is posted.
func suiteAuthorRefusals(t *testing.T, e *suiteEnv) {
	client := e.dial(t)
	for _, c := range []struct {
		name, nick, token string
		code              core.ErrorCode
		numeric           int
	}{
		{"registered-nick", "alice", "", core.NickRegistered, 498},
		{"live-nick", "watcher", "", core.NickInUse, 433},
		{"wrong-token", "alice", wrongToken, core.AuthenticationFailed, 0},
	} {
		id := e.id(c.name)
		e.admitted(t, client.call(t, sendBody(t, id, "#refusals", "refused "+c.name, c.nick, c.token)), id)
		r := e.receipt(t, id, c.nick, c.token)
		if r.State != core.Blocked || r.Failure == nil || r.Failure.Code != c.code || r.Failure.Transient || c.numeric != 0 && r.Failure.UpstreamCode != c.numeric {
			t.Fatalf("%s: %+v %+v", c.name, r, r.Failure)
		}
	}
	if messages := nativeWebTransportMessages(t, e.socket, "#refusals"); len(messages) != 0 {
		t.Fatal("a refused author posted", messages)
	}
}

// A request that is not valid contract JSON, or names a member the contract
// does not define, is refused before intake.
func suiteMalformedRefused(t *testing.T, e *suiteEnv) {
	for _, c := range []struct{ name, suffix string }{
		{"truncated", ``},
		{"unknown-member", `,"account":"other"}`},
	} {
		id := e.id(c.name)
		valid := string(sendBody(t, id, "#malformed", "must not post", e.guest("malformed"), ""))
		body := strings.TrimSuffix(valid, "}") + c.suffix
		refused(t, e.dial(t).call(t, []byte(body)), core.InvalidRequest)
		e.noJob(t, id)
	}
	if messages := nativeWebTransportMessages(t, e.socket, "#malformed"); len(messages) != 0 {
		t.Fatal("a malformed request posted", messages)
	}
}

// A listener admits only its configured targets: a request for another
// room is refused before intake.
func suiteTargetRefused(t *testing.T, e *suiteEnv) {
	id := e.id("target")
	refused(t, e.dial(t).call(t, sendBody(t, id, "#other", "must not post", e.guest("target"), "")), core.Forbidden)
	e.noJob(t, id)
}

// A request ID names one intent: a retry with a different body is refused
// and AIRC keeps the original.
func suiteConflictingRetry(t *testing.T, e *suiteEnv) {
	id, nick := e.id("conflict"), e.guest("conflict")
	original, changed := e.text("original"), e.text("changed")
	e.admitted(t, e.dial(t).call(t, sendBody(t, id, "#conflict", original, nick, "")), id)
	e.posted(t, id, "#conflict", original, nick, "")
	refused(t, e.dial(t).call(t, sendBody(t, id, "#conflict", changed, nick, "")), core.IDConflict)
	if messages := nativeWebTransportMessages(t, e.socket, "#conflict"); len(messages) != 1 || messages[0].Message != original {
		t.Fatal("a conflicting retry changed what AIRC holds", messages)
	}
}

// A client that loses the reply retries the same request, on a new
// connection where the carrier has them: AIRC holds one message.
func suiteRetryAfterLostReply(t *testing.T, e *suiteEnv) {
	id, nick, text := e.id("retry"), e.guest("retry"), e.text("retry")
	body := sendBody(t, id, "#retry", text, nick, "")
	first := e.dial(t)
	first.send(t, body)
	// AIRC posting fences the first attempt's intake before the retry.
	seen := e.observed.wait(t, text)
	first.close()
	r := e.dial(t).call(t, body)
	e.admitted(t, r, id)
	if e.carrier.results && r.result.Result.State == core.Accepted && r.result.Result.MessageID != seen.ID {
		t.Fatalf("retry answered another message: %v", r)
	}
	e.posted(t, id, "#retry", text, nick, "")
	if n := e.nativeCount(t, "#retry", text); n != 1 {
		t.Fatalf("AIRC holds %d messages for one request ID", n)
	}
}

// Idempotency belongs to the author, not the carrier: the same request on
// another carrier (the HTTP listener) is the same job.
func suiteRetryOnAnotherCarrier(t *testing.T, e *suiteEnv) {
	id, text := e.id("cross"), e.text("cross")
	body := sendBody(t, id, "#cross", text, "alice", aliceToken)
	e.admitted(t, e.dial(t).call(t, body), id)
	seen := e.posted(t, id, "#cross", text, "alice", aliceToken)
	var result core.Result
	if status := plainHTTP(t, http.MethodPost, e.receipts+"/v1/messages", body, nil, &result); status != http.StatusAccepted || result.RequestID != id || result.MessageID != seen.ID || result.SendAttempts != 1 {
		t.Fatalf("retry over HTTP: %d %+v", status, result)
	}
	if n := e.nativeCount(t, "#cross", text); n != 1 {
		t.Fatalf("AIRC holds %d messages for one request ID", n)
	}
}

// Reads are bounded pages of AIRC's own records: history and search page
// one message at a time to a caught-up cursor, and context returns the
// parent, each record equal to what aircd itself reports.
func suiteBoundedReads(t *testing.T, e *suiteEnv) {
	for i := range 2 {
		id := e.id("seed")
		plainHTTP(t, http.MethodPost, e.receipts+"/v1/messages", sendBody(t, id, "#reads", fmt.Sprintf("reads-seed %d\n", i)+strings.Repeat("世界 🙂", 200), "seeder", ""), nil, nil)
		if r := e.receipt(t, id, "seeder", ""); r.State != core.Accepted {
			t.Fatalf("seed %d: %+v", i, r)
		}
	}
	native := nativeWebTransportMessages(t, e.socket, "#reads")
	if len(native) != 2 {
		t.Fatal("seeded history", native)
	}
	client, reader := e.dial(t), e.guest("reader")
	room := map[string]any{"kind": "room", "name": "#reads"}
	for _, op := range []core.Operation{core.HistoryRead, core.Search} {
		cursor := ""
		// Two pages, then a caught-up cursor that stays caught up.
		for i := range 4 {
			fields := map[string]any{"request_id": e.id("page"), "operation": op, "target": room, "limit": 1}
			switch {
			case op == core.Search:
				fields["query"] = "reads-seed"
			case cursor == "":
				fields["start"] = "retained"
			}
			if cursor != "" {
				fields["cursor"] = cursor
			}
			r := client.call(t, mustBody(t, plainRequest(fields, reader, "")))
			if r.result == nil || r.result.Page == nil {
				t.Fatalf("%s page %d: %v", op, i, r)
			}
			page, status, want := r.result.Page, "ok", []*irc.MessageMetadata{}
			if i < 2 {
				want = native[i : i+1]
			}
			if i == 0 {
				status = "more"
			}
			if page.Gap || page.Status != status || page.Cursor == "" || len(page.Messages) != len(want) || len(want) == 1 && !reflect.DeepEqual(page.Messages[0], want[0]) {
				t.Fatalf("%s page %d: %+v, aircd holds %+v", op, i, page, want)
			}
			cursor = page.Cursor
		}
	}
	r := client.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("context"), "operation": core.ContextRead, "target": room, "parent_id": native[0].ID, "limit": 1}, reader, "")))
	if r.result == nil || r.result.Context == nil || len(r.result.Context.Messages) != 1 || !reflect.DeepEqual(&r.result.Context.Messages[0], native[0]) || r.result.Context.Summary.RootID != native[0].ID || r.result.Context.Summary.OmittedMessages != 0 {
		t.Fatalf("context: %v", r)
	}
}

// A listener with an access password (IRC's PASS) refuses a client without
// it or with a wrong one and admits the right one.
func suiteListenerPassword(t *testing.T, e *suiteEnv) {
	nick := e.guest("password")
	post := func(password string) (suiteReply, string, bool) {
		t.Helper()
		id := e.id("password")
		request := sendRequest(id, "#password", e.text(id), nick, "")
		if !e.carrier.connectPassword && password != "" {
			request["password"] = password
		}
		client, err := e.connect(t, suiteDial{address: e.guarded, password: password})
		if err != nil {
			if !e.carrier.connectPassword {
				t.Fatal(err)
			}
			return suiteReply{}, id, false
		}
		defer client.close()
		return client.call(t, mustBody(t, request)), id, true
	}
	for _, password := range []string{"", "wrong password"} {
		r, id, connected := post(password)
		if connected {
			if e.carrier.connectPassword {
				t.Fatalf("connected with password %q", password)
			}
			refused(t, r, core.Forbidden)
		}
		e.noJob(t, id)
	}
	r, id, connected := post(suitePassword)
	if !connected {
		t.Fatal("the listener password was refused")
	}
	e.admitted(t, r, id)
	e.posted(t, id, "#password", e.text(id), nick, "")
}

// A TLS listener serves the same contract to a client that trusts it.
func suiteTLSPost(t *testing.T, e *suiteEnv) {
	id, nick, text := e.id("tls"), e.guest("tls"), e.text("tls")
	e.admitted(t, e.dialWith(t, suiteDial{address: e.secure, tls: e.cert.Client.Clone()}).call(t, sendBody(t, id, "#tls", text, nick, "")), id)
	e.posted(t, id, "#tls", text, nick, "")
}

// A TLS listener negotiates TLS 1.3 with a verified chain and the
// carrier's ALPN, refuses an untrusted certificate, a wrong name and TLS
// 1.2, and answers cleartext with nothing or an alert, never with intake.
func suiteTLSRefusals(t *testing.T, e *suiteEnv) {
	trusted := e.cert.Client.Clone()
	if e.carrier.alpn != "" {
		trusted.NextProtos = []string{e.carrier.alpn}
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", e.secure, trusted)
	if err != nil {
		t.Fatal(err)
	}
	if state := conn.ConnectionState(); state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != e.carrier.alpn {
		t.Fatalf("TLS state %+v", state)
	}
	_ = conn.Close()
	wrongName, legacy := e.cert.Client.Clone(), e.cert.Client.Clone()
	wrongName.ServerName = "wrong.example"
	legacy.MinVersion, legacy.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	for name, trust := range map[string]*tls.Config{"untrusted": {MinVersion: tls.VersionTLS13}, "wrong_name": wrongName, "legacy": legacy} {
		if conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", e.secure, trust); err == nil {
			_ = conn.Close()
			t.Fatalf("%s: TLS handshake accepted", name)
		}
	}
	clear, err := net.DialTimeout("tcp", e.secure, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer clear.Close()
	id := e.id("cleartext")
	_ = clear.SetDeadline(time.Now().Add(4 * time.Second))
	_, _ = clear.Write(sendBody(t, id, "#tls", "cleartext", e.guest("tls"), ""))
	if reply, _ := io.ReadAll(clear); len(reply) > 0 && reply[0] != 0x15 {
		t.Fatalf("cleartext answered %q", reply)
	}
	e.noJob(t, id)
}

// After account.identify a connection posts as the account without naming
// it; another connection has no identified author.
func suiteIdentifyThenPost(t *testing.T, e *suiteEnv) {
	client := e.dial(t)
	r := client.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("identify"), "operation": core.AccountIdentify}, "alice", aliceToken)))
	if e.carrier.results && (r.result == nil || r.result.Account == nil || r.result.Account.Nick != "alice" || r.result.Account.AccountID != e.accountID) || !e.carrier.results && !r.acked {
		t.Fatalf("identify: %v", r)
	}
	id, text := e.id("identified"), e.text("identified")
	e.admitted(t, client.call(t, sendBody(t, id, "#identify", text, "", "")), id)
	e.posted(t, id, "#identify", text, "alice", aliceToken)
	anonymous := e.id("anonymous")
	refused(t, e.dial(t).call(t, sendBody(t, anonymous, "#identify", "no author", "", "")), core.InvalidRequest)
	e.noJob(t, anonymous)
}

func watchBody(t *testing.T, id, room, nick string) []byte {
	t.Helper()
	return mustBody(t, plainRequest(map[string]any{"request_id": id, "operation": core.Watch, "target": map[string]any{"kind": "room", "name": room}, "start": "retained", "limit": 8}, nick, ""))
}

// A streamed watch holds one native read lease, delivers a live message,
// and releases the lease when the client cancels.
func suiteWatchThenCancel(t *testing.T, e *suiteEnv) {
	if e.carrier.watchModes != nil {
		r := e.dial(t).call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("capabilities"), "operation": core.CapabilitiesGet}, e.guest("watcher"), "")))
		if r.result == nil || r.result.Capabilities == nil || !reflect.DeepEqual(r.result.Capabilities.WatchModes, e.carrier.watchModes) {
			t.Fatalf("watch modes: %v", r)
		}
	}
	baseline := nativeObserverCount(t, e.statusClient)
	pages, cancel := e.dial(t).(suiteWatcher).watch(t, watchBody(t, e.id("watch"), "#watch", e.guest("watcher")))
	if first := pages(); first == nil || first.Cursor == "" {
		t.Fatal("watch did not start", first)
	}
	waitNativeObserverCount(t, e.statusClient, baseline+1)
	id, text := e.id("live"), e.text("live")
	plainHTTP(t, http.MethodPost, e.receipts+"/v1/messages", sendBody(t, id, "#watch", text, "alice", aliceToken), nil, nil)
	for live := false; !live; {
		page := pages()
		if page == nil {
			t.Fatal("watch ended before the live message")
		}
		for _, m := range page.Messages {
			live = live || m.Message == text && m.From == "alice"
		}
	}
	cancel()
	waitNativeObserverCountAtMost(t, e.statusClient, baseline)
}

// The gateway stops promptly and cleanly with clients open (an active watch
// where the carrier streams), releases aircd's read leases and its journal,
// and the journal keeps the registered author's accepted job with the
// account ID AIRC reported.
func suiteShutdownJoins(t *testing.T, e *suiteEnv) {
	baseline := nativeObserverCount(t, e.statusClient)
	client := e.dial(t)
	id, text := e.id("shutdown"), e.text("shutdown")
	e.admitted(t, client.call(t, sendBody(t, id, "#shutdown", text, "alice", aliceToken)), id)
	seen := e.posted(t, id, "#shutdown", text, "alice", aliceToken)
	var pages func() *backend.HistoryPage
	if e.carrier.watch {
		pages, _ = e.dial(t).(suiteWatcher).watch(t, watchBody(t, e.id("shutdown-watch"), "#shutdown", e.guest("watcher")))
		if pages() == nil {
			t.Fatal("watch did not start")
		}
		waitNativeObserverCount(t, e.statusClient, baseline+1)
	}
	e.gateway.stop(t)
	if t.Failed() {
		return
	}
	if pages != nil {
		for page := pages(); page != nil; page = pages() {
		}
	}
	waitNativeObserverCountAtMost(t, e.statusClient, baseline)
	s, err := store.Open(t.Context(), e.config)
	if err != nil {
		t.Fatal("journal not released:", err)
	}
	job, err := s.LookupAuthor(t.Context(), registeredSubmission(t, "alice", aliceToken), id)
	if err != nil || job.Result.State != core.Accepted || job.Result.MessageID != seen.ID || job.Result.SendAttempts != 1 || job.AccountID != e.accountID {
		t.Fatalf("journaled job %+v (account %q, want %q): %v", job.Result, job.AccountID, e.accountID, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// A restarted gateway keeps the accepted job and never redelivers it.
	restarted := startAuthorGateway(t, e.config)
	if r := waitReceipt(t, "http://"+restarted.addresses["receipts"], id, authorHeaders("alice", aliceToken)); r.State != core.Accepted || r.MessageID != seen.ID || r.SendAttempts != 1 {
		t.Fatalf("after restart: %+v", r)
	}
	restarted.stop(t)
	if n := e.nativeCount(t, "#shutdown", text); n != 1 {
		t.Fatalf("AIRC holds %d messages after a restart", n)
	}
}

// suiteContext bounds one client exchange.
func suiteContext(t *testing.T) (context.Context, context.CancelFunc) {
	return context.WithTimeout(t.Context(), 10*time.Second)
}
