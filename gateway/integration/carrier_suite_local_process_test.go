//go:build darwin || linux

package gateway_test

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestLocalProcessCarriers runs the cross-carrier suite's scenarios against
// the local carriers that run as their own gateway process: a child on its
// parent's pipe, socket pair or inherited Unix stream, and the one-shot file
// mailbox. Each process owns the journal while it runs, so these carriers
// cannot share TestCarrierSuite's in-process gateway and HTTP receipts. The
// parent-owned endpoint or the private directory is the access gate, so
// there is no listener password or TLS twin to test. Failures read
// TestLocalProcessCarriers/<carrier>/<scenario>; behaviour particular to
// one carrier (signals, crash recovery, partial frames, file modes) keeps
// its focused test in the carrier's own files.
func TestLocalProcessCarriers(t *testing.T) {
	for _, c := range localProcessCarriers {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newLocalProcessEnv(t, c)
			for _, s := range localProcessScenarios {
				if s.applies == nil || s.applies(c) {
					t.Run(s.name, func(t *testing.T) { s.run(t, e) })
				}
			}
		})
	}
}

// localProcessCarrier is one row of the table.
type localProcessCarrier struct {
	name     string
	listener config.Listener
	// start provisions the carrier in the environment's root and starts it.
	start func(t *testing.T, e *localProcessEnv) localSession
	// frames: requests travel in correlated plain frames, so an invalid
	// request is refused in its own frame. reads: the carrier serves
	// history.read.
	frames, reads bool
	// wire, if set, runs carrier-specific checks as the "wire" subtest.
	wire func(t *testing.T, e *localProcessEnv)
}

var localProcessCarriers = []localProcessCarrier{
	{name: "stdio", listener: config.Listener{Protocol: "stdio"}, start: startStdioSession, frames: true, reads: true},
	{name: "socketpair", listener: config.Listener{Protocol: "socketpair"}, start: startParentSession("socketpair"), frames: true, reads: true},
	{name: "inherited", listener: config.Listener{Protocol: "inherited"}, start: startParentSession("inherited"), frames: true, reads: true},
	{name: "filemailbox", listener: config.Listener{Protocol: "file_mailbox", Operations: mailboxOperations}, start: startFileMailboxSession, frames: true, reads: true, wire: fileMailboxBoundAuthor},
}

// localRoom is the one room the carriers admit.
const localRoom = "#lobby"

var localProcessScenarios = []struct {
	name    string
	applies func(localProcessCarrier) bool
	run     func(*testing.T, *localProcessEnv)
}{
	{"guest_post", nil, localGuestPost},
	{"registered_post", nil, localRegisteredPost},
	{"refused_before_intake", nil, localRefusedBeforeIntake},
	{"replay_posts_once", nil, localReplayPostsOnce},
	{"bounded_reads", func(c localProcessCarrier) bool { return c.reads }, localBoundedReads},
	{"wire", func(c localProcessCarrier) bool { return c.wire != nil }, func(t *testing.T, e *localProcessEnv) { e.carrier.wire(t, e) }},
	{"shutdown_joins", nil, localShutdownJoins},
}

// localSession submits plain requests to a running carrier. stop ends it
// (EOF) and waits for a clean exit; the one-shot mailbox has already exited
// after each call.
type localSession interface {
	call(t *testing.T, request []byte) localOutcome
	stop(t *testing.T)
}

// localOutcome is a carrier's answer to one request.
type localOutcome struct {
	result  *dispatch.DetachedResult // the operation's result
	failure *core.Failure            // a correlated refusal
	refused bool                     // no answer: closed, or the mailbox refused its input
}

func (o localOutcome) String() string {
	return fmt.Sprintf("result=%+v failure=%+v refused=%t", o.result, o.failure, o.refused)
}

// localProcessEnv is one carrier's aircd, configuration and running session.
type localProcessEnv struct {
	carrier           localProcessCarrier
	socket, accountID string
	root              string
	config            config.Config
	binary            string
	observed          *suiteObserver
	session           localSession
	sequence          int
	// refused are request IDs that must never reach intake. A running
	// carrier owns the journal, so shutdown_joins checks them once it has
	// released it.
	refused []string
}

func newLocalProcessEnv(t *testing.T, c localProcessCarrier) *localProcessEnv {
	t.Helper()
	socket, gatewayToken := currentNative(t)
	provision := dial(t, socket, "alice", aliceToken, true, true)
	accountID := provision.AccountID()
	_ = provision.Close()
	watcher := dialObserver(t, socket)
	if err := watcher.Join(localRoom); err != nil {
		t.Fatal(err)
	}
	next(t, watcher, func(e *irc.JoinEvent) bool { return e.Agent == "watcher" })
	root := localSuiteDir(t)
	cfg := config.Defaults()
	cfg.Version, cfg.Realm, cfg.StateDir = 1, "local-process", filepath.Join(root, "state")
	if err := os.Mkdir(cfg.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(cfg.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	cfg.Upstream = &config.Upstream{Network: "unix", Address: socket, GatewayTokenFile: gatewayToken}
	l := c.listener
	l.ID, l.Enabled, l.Limits, l.Targets = c.name, true, fixtureListenerLimits(), []core.Target{{Kind: "room", Name: localRoom}}
	if l.Operations == nil {
		l.Operations = []core.Operation{} // every implemented operation
	}
	cfg.Listeners = []config.Listener{l}
	e := &localProcessEnv{carrier: c, socket: socket, accountID: accountID, root: root, config: cfg, binary: currentGatewayBinary(t), observed: observe(watcher)}
	e.session = c.start(t, e)
	return e
}

// writeConfig saves the configuration for a gateway command.
func (e *localProcessEnv) writeConfig(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(e.config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.root, "gateway.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// id is a request ID unique to the environment.
func (e *localProcessEnv) id(name string) string {
	e.sequence++
	return fmt.Sprintf("%s-%s-%d", e.carrier.name, name, e.sequence)
}

func (e *localProcessEnv) guest(name string) string { return e.carrier.name + "-" + name }

// text is a whole multi-line Unicode body naming the carrier, with shell
// syntax no gateway command may execute.
func (e *localProcessEnv) text(name string) string {
	return fmt.Sprintf("%s via %s\nwhole body café 🙂 $(touch must-not-execute)", name, e.carrier.name)
}

func (e *localProcessEnv) send(t *testing.T, id, text, nick, token string) localOutcome {
	t.Helper()
	return e.session.call(t, sendBody(t, id, localRoom, text, nick, token))
}

// admitted checks that an outcome durably admits request id.
func (e *localProcessEnv) admitted(t *testing.T, o localOutcome, id string) {
	t.Helper()
	if o.result == nil || o.result.Result == nil || o.result.Result.RequestID != id || !o.result.Result.Durable {
		t.Fatalf("%s not admitted: %v", id, o)
	}
}

// posted waits for AIRC to post text and checks its author and room.
func (e *localProcessEnv) posted(t *testing.T, text, nick string) *irc.MessageEvent {
	t.Helper()
	seen := e.observed.wait(t, text)
	if seen.From != nick || seen.Target != localRoom {
		t.Fatalf("AIRC posted %+v, want %s in %s", seen, nick, localRoom)
	}
	return seen
}

// noJob records that the journal must hold no job for id.
func (e *localProcessEnv) noJob(id string) { e.refused = append(e.refused, id) }

// A guest names only a nickname; the body spans several frames' worth.
func localGuestPost(t *testing.T, e *localProcessEnv) {
	id, nick, text := e.id("guest"), e.guest("guest"), e.text("guest")+"\n"+strings.Repeat("世界 🙂", 200)
	e.admitted(t, e.send(t, id, text, nick, ""), id)
	e.posted(t, text, nick)
}

// A registered author names its token: AIRC posts as the account.
func localRegisteredPost(t *testing.T, e *localProcessEnv) {
	id, text := e.id("registered"), e.text("registered")
	e.admitted(t, e.send(t, id, text, "alice", aliceToken), id)
	e.posted(t, text, "alice")
}

// A request for a room the listener does not admit, or one that names a
// member the contract does not define, is refused before intake; a frame
// carrier answers in the request's own frame and serves on.
func localRefusedBeforeIntake(t *testing.T, e *localProcessEnv) {
	refusedWith := func(o localOutcome, code core.ErrorCode) {
		t.Helper()
		t.Logf("refused: %v", o)
		if !o.refused && (o.failure == nil || o.failure.Code != code) {
			t.Fatalf("want a %s refusal: %v", code, o)
		}
	}
	id := e.id("target")
	refusedWith(e.session.call(t, sendBody(t, id, "#other", "must not post", e.guest("target"), "")), core.Forbidden)
	e.noJob(id)
	if e.carrier.frames {
		id := e.id("malformed")
		body := sendBody(t, id, localRoom, "must not post", e.guest("malformed"), "")
		o := e.session.call(t, append(body[:len(body)-1], `,"account":"other"}`...))
		if o.failure == nil || o.failure.Code != core.InvalidRequest {
			t.Fatalf("want an invalid_request answer: %v", o)
		}
		e.noJob(id)
	}
}

// Submitting a request again, as a client that lost the answer does,
// returns the original acceptance, and AIRC holds one message.
func localReplayPostsOnce(t *testing.T, e *localProcessEnv) {
	id, nick, text := e.id("replay"), e.guest("replay"), e.text("replay")
	e.admitted(t, e.send(t, id, text, nick, ""), id)
	seen := e.posted(t, text, nick)
	r := acceptedReceipt(t, 10*time.Second, 50*time.Millisecond, "the replay to report acceptance", func() *core.Result {
		o := e.send(t, id, text, nick, "")
		if o.result == nil || o.result.Result == nil || o.result.Result.RequestID != id {
			t.Fatalf("replay: %v", o)
		}
		return o.result.Result
	})
	if r.MessageID != seen.ID || r.SendAttempts != 1 {
		t.Fatalf("replay answered another delivery: %+v", r)
	}
	if n := (&suiteEnv{socket: e.socket}).nativeCount(t, localRoom, text); n != 1 {
		t.Fatalf("AIRC holds %d messages for one request ID", n)
	}
}

// Reads are bounded pages of AIRC's own records.
func localBoundedReads(t *testing.T, e *localProcessEnv) {
	reader := e.guest("reader")
	checkHistoryPages(t, nativeWebTransportMessages(t, e.socket, localRoom), func(fields map[string]any) *backend.HistoryPage {
		fields["request_id"] = e.id("page")
		if o := e.session.call(t, mustBody(t, plainRequest(fields, reader, ""))); o.result != nil {
			return o.result.Page
		}
		return nil
	})
}

// The carrier ends cleanly (a stream at EOF, the mailbox when it returns),
// joins delivery and releases the journal, which
// keeps the registered author's accepted job with its account ID and holds
// no job for any refused request.
func localShutdownJoins(t *testing.T, e *localProcessEnv) {
	id, text := e.id("shutdown"), e.text("shutdown")
	e.admitted(t, e.send(t, id, text, "alice", aliceToken), id)
	seen := e.posted(t, text, "alice")
	e.session.stop(t)
	if t.Failed() {
		return
	}
	s, err := store.Open(t.Context(), e.config)
	if err != nil {
		t.Fatal("journal not released:", err)
	}
	job, err := s.LookupAuthor(t.Context(), registeredSubmission(t, "alice", aliceToken), id)
	pending, pendingErr := s.HasPending(t.Context())
	if closeErr := errors.Join(pendingErr, s.Close()); closeErr != nil || pending {
		t.Fatal("delivery did not join before the journal was released:", pending, closeErr)
	}
	if err != nil || job.Result.State != core.Accepted || job.Result.MessageID != seen.ID || job.Result.SendAttempts != 1 || job.AccountID != e.accountID {
		t.Fatalf("journaled job %+v (account %q, want %q): %v", job.Result, job.AccountID, e.accountID, err)
	}
	for _, id := range e.refused {
		(&suiteEnv{config: e.config}).noJob(t, id)
	}
}

// localStream exchanges length-prefixed correlated plain frames with a
// stream carrier.
type localStream struct {
	in  io.Writer
	out interface {
		io.Reader
		SetReadDeadline(time.Time) error
	}
	done func(t *testing.T)
}

func (s *localStream) call(t *testing.T, request []byte) localOutcome {
	t.Helper()
	id := localSuiteID("call")
	frame := localSuiteFrame(id, request, false)
	if _, err := s.in.Write(binary.BigEndian.AppendUint32(nil, uint32(len(frame)))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.in.Write(frame); err != nil {
		t.Fatal(err)
	}
	if err := s.out.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	_, err := io.ReadFull(s.out, header[:])
	if errors.Is(err, io.EOF) {
		return localOutcome{refused: true}
	}
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(s.out, body); err != nil {
		t.Fatal(err)
	}
	return localFrameOutcome(t, id, body)
}

func (s *localStream) stop(t *testing.T) { s.done(t) }

// localFrameOutcome decodes the reply to frame id; an undecodable frame is
// refused without a correlation ID.
func localFrameOutcome(t *testing.T, id string, body []byte) localOutcome {
	t.Helper()
	var response adapter.FrameResponse
	if err := json.Unmarshal(body, &response); err != nil || response.CorrelationID != id && (response.CorrelationID != "" || response.Error == nil) {
		t.Fatalf("reply to %s: %s %v", id, body, err)
	}
	if response.Error != nil {
		return localOutcome{failure: response.Error}
	}
	return localOutcome{result: response.Result}
}

// A serve-stdio child reads frames from its parent's pipe and exits at EOF.
func startStdioSession(t *testing.T, e *localProcessEnv) localSession {
	p := startStdio(t, e.binary, e.config)
	return &localStream{in: p.stdin, out: p.stdout.(*os.File), done: func(t *testing.T) {
		_ = p.stdin.Close()
		if err := p.wait(); err != nil {
			t.Errorf("serve-stdio at EOF: %v %s", err, p.stderr.String())
		}
	}}
}

// serve-socketpair and serve-inherited take a stream from their parent as
// file descriptor 3 and exit at EOF.
func startParentSession(protocol string) func(*testing.T, *localProcessEnv) localSession {
	return func(t *testing.T, e *localProcessEnv) localSession {
		conn, stop, _ := startParentFD(t, e.binary, e.config, protocol, 0, false)
		if address, ok := conn.RemoteAddr().(*net.UnixAddr); protocol == "inherited" && (!ok || address.Name == "") {
			t.Fatal("the child did not inherit a named Unix endpoint", conn.RemoteAddr())
		}
		return &localStream{in: conn, out: conn, done: func(*testing.T) {
			_ = conn.(*net.UnixConn).CloseWrite()
			stop()
		}}
	}
}

// fileMailboxSession runs mailbox-once on one request in its private
// directory per call.
type fileMailboxSession struct {
	e                          *localProcessEnv
	directory, path, tokenFile string
}

func startFileMailboxSession(t *testing.T, e *localProcessEnv) localSession {
	s := &fileMailboxSession{e: e, directory: filepath.Join(e.root, "mailbox"), tokenFile: filepath.Join(e.root, "alice.token")}
	if err := os.Mkdir(s.directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.tokenFile, []byte(aliceToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e.config.Listeners[0].Address = s.directory
	e.config.Listeners[0].BoundAuthor = &config.BoundAuthor{Nick: "alice", AccountTokenFile: s.tokenFile}
	s.path = e.writeConfig(t)
	return s
}

func (s *fileMailboxSession) call(t *testing.T, request []byte) localOutcome {
	t.Helper()
	id := localSuiteID("call")
	if err := os.WriteFile(filepath.Join(s.directory, "request.json"), localSuiteFrame(id, request, false), 0600); err != nil {
		t.Fatal(err)
	}
	output, runErr := exec.Command(s.e.binary, "mailbox-once", "--file", s.path).CombinedOutput()
	body, err := os.ReadFile(filepath.Join(s.directory, "response.json"))
	if errors.Is(err, os.ErrNotExist) && runErr != nil {
		t.Logf("mailbox-once: %v %s", runErr, output)
		return localOutcome{refused: true}
	}
	if err != nil || runErr != nil {
		t.Fatalf("mailbox-once: %v %v %s", runErr, err, output)
	}
	if err := os.Remove(filepath.Join(s.directory, "response.json")); err != nil {
		t.Fatal(err)
	}
	return localFrameOutcome(t, id, body)
}

func (s *fileMailboxSession) stop(*testing.T) {}

// fileMailboxBoundAuthor: a request without an author runs as the
// listener's bound author, whose token stays in an owner-only file instead
// of in every request.
func fileMailboxBoundAuthor(t *testing.T, e *localProcessEnv) {
	id, text := e.id("bound"), e.text("bound")
	e.admitted(t, e.send(t, id, text, "", ""), id)
	e.posted(t, text, "alice")
}
