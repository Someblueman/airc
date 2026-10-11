package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testserver"
	"github.com/Someblueman/airc/pkg/irc"
)

// fastReconnect shrinks the watcher's first retry delay so restart tests do
// not wait out the production default.
func fastReconnect(t testing.TB) {
	t.Helper()
	old := watchMinBackoff
	watchMinBackoff = 20 * time.Millisecond
	t.Cleanup(func() { watchMinBackoff = old })
}

// serverClockSkew moves every test daemon's clock ahead of real time.
var serverClockSkew atomic.Int64

// advanceServerClock lets a test pass an expiry (presence, signals, slow mode,
// mutes) without waiting for it. The skew is undone when the test ends.
func advanceServerClock(t testing.TB, d time.Duration) {
	t.Helper()
	serverClockSkew.Add(int64(d))
	t.Cleanup(func() { serverClockSkew.Add(-int64(d)) })
}

// testDaemonConfig makes cfg quiet and cheap: no ping or read deadlines to
// race the test, and no fsync per append.
func testDaemonConfig(cfg server.Config) server.Config {
	cfg.PingInterval, cfg.ReadTimeout = time.Hour, time.Hour
	cfg.Sync = atomicfile.SyncNone
	return cfg
}

func cliTestServer(t testing.TB) string {
	t.Helper()
	return cliTestServerWith(t, server.Config{HistoryLimit: 16})
}

func cliTestServerWith(t testing.TB, cfg server.Config) string {
	return cliTestServerSetup(t, cfg, nil)
}

func cliTestServerSetup(t testing.TB, cfg server.Config, setup func(*server.Server) error) string {
	t.Helper()
	cfg = testDaemonConfig(cfg)
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().Add(time.Duration(serverClockSkew.Load())) }
	}
	return testserver.Start(t, cfg, setup)
}

// restartableServer is a daemon whose address and history file outlive it, so
// a test can stop it and bring the same daemon back.
type restartableServer struct {
	srv  *server.Server
	done chan error
	addr string
	cfg  server.Config
	file string
	once sync.Once
}

// startServerAt serves cfg on address ("127.0.0.1:0" for a fresh port),
// restoring and persisting history in historyFile. It listens itself because
// testserver.Start cannot rebind a known address.
func startServerAt(t testing.TB, address string, cfg server.Config, historyFile string) *restartableServer {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	cfg = testDaemonConfig(cfg)
	cfg.Logger = testserver.Logger()
	srv := server.New(cfg)
	if err := srv.RestoreHistory(historyFile); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	s := &restartableServer{srv: srv, done: make(chan error, 1), addr: listener.Addr().String(), cfg: cfg, file: historyFile}
	go func() { s.done <- srv.Serve(listener) }()
	t.Cleanup(s.stop)
	return s
}

// stop is idempotent: tests stop a daemon explicitly and Cleanup stops it again.
func (s *restartableServer) stop() {
	s.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(ctx)
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
	})
}

// restart stops the daemon, lets missed run against a temporary daemon on the
// same history (so those messages can only reach clients by catch-up, never
// live), and then serves the original address again.
func (s *restartableServer) restart(t testing.TB, missed func(address string)) *restartableServer {
	t.Helper()
	s.stop()
	if missed != nil {
		seed := startServerAt(t, "127.0.0.1:0", s.cfg, s.file)
		missed(seed.addr)
		seed.stop()
	}
	return startServerAt(t, s.addr, s.cfg, s.file)
}

// eventually polls condition until it holds. It is a real synchronisation
// point: the deadline is generous and only bounds a failure, never a success.
func eventually(t testing.TB, within time.Duration, what string, condition func() bool) {
	t.Helper()
	eventuallyEvery(t, within, 2*time.Millisecond, what, condition)
}

// eventuallyEvery is eventually with a caller-chosen polling interval, for
// conditions that cost a daemon round trip to evaluate.
func eventuallyEvery(t testing.TB, within, interval time.Duration, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", within, what)
		}
		time.Sleep(interval)
	}
}

// liveEventWait bounds a test's wait for one event on a live client. It only
// bounds a failure: a passing wait returns as soon as the event arrives.
const liveEventWait = 10 * time.Second

// awaitClientEvent returns the first event from client that match accepts. A
// closed event stream is an error, never a nil event to loop on.
func awaitClientEvent(ctx context.Context, client *irc.Client, match func(irc.Event) bool) (irc.Event, error) {
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return nil, errors.New("client event stream closed")
			}
			if match(event) {
				return event, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// nextLiveEvent is awaitClientEvent bounded by liveEventWait; what names the
// event in the failure.
func nextLiveEvent(t testing.TB, client *irc.Client, what string, match func(irc.Event) bool) irc.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveEventWait)
	defer cancel()
	event, err := awaitClientEvent(ctx, client, match)
	if err != nil {
		t.Fatalf("waiting for %s: %v", what, err)
	}
	return event
}

// joinLive joins channel and waits for the server to finish the join (its end
// of NAMES). Client.Join only writes the command, so without this a message
// another connection sends next can reach the channel before the member does.
func joinLive(t testing.TB, client *irc.Client, channel string) {
	t.Helper()
	if err := client.Join(channel); err != nil {
		t.Fatal(err)
	}
	nextLiveEvent(t, client, "the server to finish JOIN "+channel, func(event irc.Event) bool {
		raw, ok := event.(*irc.RawEvent)
		return ok && raw.Command == "366" && len(raw.Params) > 1 && strings.EqualFold(raw.Params[1], channel)
	})
}

// cli runs one airc command against address and returns its output.
func cli(t testing.TB, address string, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(append(args, "--addr", address), strings.NewReader(""), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func mustCLI(t testing.TB, address string, args ...string) string {
	t.Helper()
	stdout, stderr, err := cli(t, address, args...)
	if err != nil {
		t.Fatalf("airc %s: %v (%s)", strings.Join(args, " "), err, stderr)
	}
	return stdout
}

// cliJSON runs a command with --json and decodes its single JSON document.
func cliJSON[T any](t testing.TB, address string, args ...string) T {
	t.Helper()
	out := mustCLI(t, address, append(args, "--json")...)
	var value T
	if err := json.Unmarshal([]byte(out), &value); err != nil {
		t.Fatalf("airc %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return value
}

// jsonLines decodes output made of one JSON document per line.
func jsonLines[T any](t testing.TB, output string) []T {
	t.Helper()
	var rows []T
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var row T
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("bad JSON line %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

// checkMessages returns the messages of a `check --json` output, without its
// header and status records.
func checkMessages(t testing.TB, output string) []checkMessage {
	t.Helper()
	var messages []checkMessage
	for _, m := range jsonLines[checkMessage](t, output) {
		if m.Type == "message" {
			messages = append(messages, m)
		}
	}
	return messages
}

func checkBodies(t testing.TB, output string) []string {
	t.Helper()
	var bodies []string
	for _, m := range checkMessages(t, output) {
		bodies = append(bodies, m.Message)
	}
	return bodies
}

// checkFooter returns the closing status record of a `check --json` output.
func checkFooter(t testing.TB, output string) checkStatus {
	t.Helper()
	var status checkStatus
	for _, value := range jsonLines[checkStatus](t, output) {
		if value.Type == "status" {
			status = value
		}
	}
	return status
}

// send posts body to channel as nick and returns the server's confirmation.
func send(t testing.TB, address, nick, channel, body string) irc.MessageEvent {
	t.Helper()
	return cliJSON[irc.MessageEvent](t, address, "send", "--nick", nick, "--channel", channel, "--message", body)
}

// agentEnv gives the test an empty state directory and no default identity.
func agentEnv(t testing.TB) {
	t.Helper()
	t.Setenv("AIRC_STATE_DIR", t.TempDir())
	t.Setenv("AIRC_NICK", "")
	t.Setenv("AIRC_CHANNEL", "")
}

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visible strips terminal styling so assertions read the text a person sees.
func visible(text string) string { return sgr.ReplaceAllString(text, "") }

// waitForObservation waits until the server has acknowledged every target in a
// comma-separated list; the server confirms each one separately.
func waitForObservation(ctx context.Context, client *irc.Client, targets string) error {
	return awaitObservation(ctx, client, len(strings.Split(targets, ",")))
}

func TestAgentsJSONIncludesLivePresence(t *testing.T) {
	address := cliTestServer(t)
	bot, err := irc.Dial(irc.Config{Nick: "builder", Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	defer bot.Close()
	joinLive(t, bot, "#build")
	var stdout, stderr bytes.Buffer
	args := []string{"agents", "--nick", "observer", "--addr", address, "--json"}
	if err := run(args, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run agents: %v (%s)", err, stderr.String())
	}
	var agents []irc.AgentInfo
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &agents); err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if agent.Nick == "builder" {
			if len(agent.Channels) != 1 || agent.Channels[0] != "#build" {
				t.Fatalf("unexpected builder presence: %#v", agent)
			}
			return
		}
	}
	t.Fatalf("agents response omitted builder: %s", stdout.String())
}

func TestWaitForObservationAcknowledgesEveryTarget(t *testing.T) {
	for name, targets := range map[string]string{
		"single target":        "#watch",
		"comma-separated list": "#one,#two,@watcher",
	} {
		t.Run(name, func(t *testing.T) {
			client, err := irc.Dial(irc.Config{Nick: "watcher", Addr: cliTestServer(t)})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if err := client.Raw("OBSERVE " + targets); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := waitForObservation(ctx, client, targets); err != nil {
				t.Fatalf("waitForObservation(%q): %v", targets, err)
			}
		})
	}
}

func TestTopLevelHelpShowsTheCommandSummary(t *testing.T) {
	for _, arg := range []string{"--help", "-h", "help"} {
		var stdout, stderr bytes.Buffer
		if err := run([]string{arg}, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("%s: %v", arg, err)
		}
		for _, want := range []string{"airc check", "airc send", "airc skill", "airc watch"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("airc %s output lacks %q:\n%s", arg, want, stdout.String())
			}
		}
	}
	// A subcommand's own flags must not show the default twice.
	var stderr bytes.Buffer
	_ = run([]string{"send", "-h"}, strings.NewReader(""), io.Discard, &stderr)
	if strings.Count(stderr.String(), "(default") > strings.Count(stderr.String(), "\n  -") {
		t.Errorf("flag help repeats defaults:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "(default $AIRC") {
		t.Errorf("flag help still describes an env default as a default:\n%s", stderr.String())
	}
}

func TestChannelsMayBeGivenWithoutTheHash(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "send", "--nick", "writer", "--channel", "bare", "--message", "to bare")
	if out := mustCLI(t, address, "history", "#bare", "--json"); !strings.Contains(out, "to bare") {
		t.Fatalf("a bare channel name did not reach #bare: %s", out)
	}
	if got := checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "bare", "--json")); len(got) != 1 || got[0] != "to bare" {
		t.Fatalf("check --channel bare = %v", got)
	}
	if out := mustCLI(t, address, "search", "bare", "--target", "bare", "--json"); !strings.Contains(out, "to bare") {
		t.Fatalf("search --target bare found nothing: %q", out)
	}
	if out := mustCLI(t, address, "names", "bare"); !strings.Contains(out, "#bare") {
		t.Fatalf("names did not normalise the channel: %q", out)
	}
	out, _ := startWatch(t, "--channel", "bare,@Me", "--color", "never", "--backlog", "5", "--addr", address)
	waitForOutput(t, out, "to bare")
	if !strings.Contains(out.String(), "Watching #bare, @Me") {
		t.Fatalf("watch did not normalize its targets:\n%s", out.String())
	}
	for in, want := range map[string]string{"room": "#room", "#room": "#room", "&local": "&local", "@nick": "@nick", " spaced ": "#spaced", "": ""} {
		if got := channelName(in); got != want {
			t.Errorf("channelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMissingChannelValueExplainsShellQuoting(t *testing.T) {
	var stderr bytes.Buffer
	err := run([]string{"watch", "-channel"}, strings.NewReader(""), io.Discard, &stderr)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := explain(err); !strings.Contains(got, "quoted") || !strings.Contains(got, "--channel room") {
		t.Fatalf("no quoting hint: %q", got)
	}
	if got := explain(io.EOF); got != "EOF" {
		t.Fatalf("unrelated errors must be untouched, got %q", got)
	}
}

func TestFlagErrorPrintsOnceWithHints(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		line, hint string
	}{
		{[]string{"--nick", "admin", "--watch"}, "flag provided but not defined: -watch", "hint: watch is a command, not a flag; put it first: airc watch"},
		{[]string{"watch", "-channel"}, "flag needs an argument: -channel", "--channel room"},
	} {
		var stderr bytes.Buffer
		err := run(tc.args, strings.NewReader(""), io.Discard, &stderr)
		if err == nil {
			t.Fatalf("%v: expected an error", tc.args)
		}
		writeFailure(&stderr, err)
		if n := strings.Count(stderr.String(), tc.line); n != 1 {
			t.Errorf("%v: %q printed %d times:\n%s", tc.args, tc.line, n, stderr.String())
		}
		if !strings.Contains(stderr.String(), tc.hint) {
			t.Errorf("%v: missing hint %q:\n%s", tc.args, tc.hint, stderr.String())
		}
	}
}

func TestCommandHintsNameRealCommands(t *testing.T) {
	for _, name := range commands {
		err := dispatch([]string{name, "-h"}, strings.NewReader(""), io.Discard, io.Discard)
		if err != nil && strings.Contains(err.Error(), "unknown command") {
			t.Errorf("%s is listed in commands but dispatch does not know it", name)
		}
	}
	if got := hints(errors.New("flag provided but not defined: -bogus")); len(got) != 0 {
		t.Errorf("unknown flags that are not commands got hints: %q", got)
	}
}

func TestInteractiveRejectsStrayArgumentsBeforeConnecting(t *testing.T) {
	for args, want := range map[string]string{
		"--nick admin watch":                       "watch is a command and must come first: airc watch --nick admin",
		"--nick admin --channel #ops watch --json": "airc watch --nick admin --channel '#ops' --json",
		"--nick admin hello":                       `unexpected argument "hello"`,
	} {
		// No server listens here, so reaching the dial would fail differently.
		err := run(append(strings.Fields(args), "--addr", "127.0.0.1:1"), strings.NewReader(""), io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("airc %s: err = %v, want %q", args, err, want)
		}
	}
}

// historyIDs returns the ids of the records in `history --json` style output.
func historyIDs(t testing.TB, text string) []string {
	t.Helper()
	var ids []string
	for _, message := range jsonLines[irc.HistoryEvent](t, text) {
		if message.ID != "" {
			ids = append(ids, message.ID)
		}
	}
	return ids
}
