package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

// cli runs one airc command against address and returns its output.
func cli(t *testing.T, address string, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(append(args, "--addr", address), strings.NewReader(""), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func mustCLI(t *testing.T, address string, args ...string) string {
	t.Helper()
	stdout, stderr, err := cli(t, address, args...)
	if err != nil {
		t.Fatalf("airc %s: %v (%s)", strings.Join(args, " "), err, stderr)
	}
	return stdout
}

func agentEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AIRC_STATE_DIR", t.TempDir())
	t.Setenv("AIRC_NICK", "")
	t.Setenv("AIRC_CHANNEL", "")
}

func checkBodies(t *testing.T, output string) []string {
	t.Helper()
	var bodies []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var message checkMessage
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatalf("bad JSON line %q: %v", line, err)
		}
		if message.Type != "message" {
			continue
		}
		bodies = append(bodies, message.Message)
	}
	return bodies
}

func send(t *testing.T, address, nick, channel, body string) {
	t.Helper()
	mustCLI(t, address, "send", "--nick", nick, "--channel", channel, "--message", body)
}

func TestSendNeverCollidesWithALiveSessionOrAnnouncesPresence(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	live, err := irc.Dial(irc.Config{Nick: "carol", Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := live.Join("#room"); err != nil {
		t.Fatal(err)
	}
	// Same nick as the live session, into a channel it is in.
	mustCLI(t, address, "send", "--nick", "carol", "--channel", "#room", "--message", "from a one-shot")
	for {
		event := <-live.Events()
		switch e := event.(type) {
		case *irc.MessageEvent:
			if e.Message == "from a one-shot" {
				return
			}
		case *irc.QuitEvent:
			t.Fatalf("one-shot send announced %#v", e)
		case *irc.PartEvent:
			t.Fatalf("one-shot send announced %#v", e)
		}
	}
}

func TestSendReportsQueuedDirectMessages(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	out := mustCLI(t, address, "send", "--nick", "planner", "--to", "builder", "--message", "task 7", "--json")
	var result struct {
		Message   string `json:"message"`
		Delivered *bool  `json:"delivered"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Delivered == nil || *result.Delivered || result.Message != "task 7" {
		t.Fatalf("offline DM result = %s", out)
	}
	if out := mustCLI(t, address, "send", "--nick", "planner", "--to", "builder", "--message", "task 8"); !strings.Contains(out, "queued") {
		t.Fatalf("human output should say the message was queued: %q", out)
	}
	// The recipient reads it with check, without ever having been connected.
	got := checkBodies(t, mustCLI(t, address, "check", "--nick", "builder", "--json"))
	if fmt.Sprint(got) != "[task 7 task 8]" {
		t.Fatalf("inbox = %v", got)
	}
}

func TestSendSurfacesServerErrorsImmediately(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	start := time.Now()
	_, _, err := cli(t, address, "send", "--nick", "a", "--channel", "#", "--message", "x")
	if err == nil || !strings.Contains(err.Error(), "no such channel") {
		t.Fatalf("invalid channel error = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("error took %s; it should not wait for the confirmation timeout", time.Since(start))
	}
	bare := cliTestServerWith(t, server.Config{})
	_, _, err = cli(t, bare, "send", "--nick", "a", "--to", "nobody", "--message", "x")
	if err == nil || !strings.Contains(err.Error(), "--history") {
		t.Fatalf("unknown nick without history error = %v", err)
	}
	if _, _, err := cli(t, address, "send", "--nick", "a", "--channel", "#c", "--message", " \n "); err == nil {
		t.Fatal("a blank message should be rejected")
	}
}

func TestMultilineMessagesViaArgumentAndStdin(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "send", "--nick", "writer", "--channel", "#room", "--message", "from arg\nsecond line")

	var stdout, stderr bytes.Buffer
	report := "Report\n\n- one\n- two\n"
	if err := run([]string{"send", "--nick", "writer", "--channel", "#room", "--message", "-", "--addr", address, "--json"}, strings.NewReader(report), &stdout, &stderr); err != nil {
		t.Fatalf("send from stdin: %v (%s)", err, stderr.String())
	}
	var sent irc.MessageEvent
	if err := json.Unmarshal(stdout.Bytes(), &sent); err != nil || sent.Message != "Report\n\n- one\n- two" {
		t.Fatalf("stdin send result = %q, %v", sent.Message, err)
	}

	out := mustCLI(t, address, "check", "--nick", "me", "--channel", "#room", "--json")
	var got []checkMessage
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var m checkMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("one JSON object per message must survive embedded newlines: %q", line)
		}
		if m.Type == "message" {
			got = append(got, m)
		}
	}
	if len(got) != 2 || got[0].Message != "from arg\nsecond line" || got[1].Message != "Report\n\n- one\n- two" {
		t.Fatalf("check returned %#v", got)
	}
	human := mustCLI(t, address, "history", "#room")
	if !strings.Contains(human, "writer: from arg\n  second line\n") {
		t.Fatalf("continuation lines should be indented in human output:\n%s", human)
	}
}

func TestSendUsesEnvironmentDefaults(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	t.Setenv("AIRC_NICK", "envbot")
	t.Setenv("AIRC_CHANNEL", "#envroom")
	mustCLI(t, address, "send", "--message", "hello from env")
	out := mustCLI(t, address, "history", "#envroom", "--json")
	if !strings.Contains(out, `"from":"envbot"`) || !strings.Contains(out, "hello from env") {
		t.Fatalf("history = %s", out)
	}
	// --to must win over a default channel.
	mustCLI(t, address, "send", "--to", "someone", "--message", "direct")
}

func TestCheckReturnsEachMessageOnceAndPeekDoesNotConsume(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	check := func(extra ...string) []string {
		return checkBodies(t, mustCLI(t, address, append([]string{"check", "--nick", "me", "--channel", "#room", "--json"}, extra...)...))
	}
	send(t, address, "writer", "#room", "one")
	send(t, address, "writer", "#room", "two")
	if got := check(); fmt.Sprint(got) != "[one two]" {
		t.Fatalf("first check = %v", got)
	}
	if got := check(); len(got) != 0 {
		t.Fatalf("second check should be empty, got %v", got)
	}
	send(t, address, "writer", "#room", "three")
	send(t, address, "me", "#room", "my own message") // never echoed back to me
	send(t, address, "writer", "#room", "four")
	if got := check("--peek"); fmt.Sprint(got) != "[three four]" {
		t.Fatalf("peek = %v", got)
	}
	if got := check("--peek"); fmt.Sprint(got) != "[three four]" {
		t.Fatalf("peek must not consume: %v", got)
	}
	if got := check(); fmt.Sprint(got) != "[three four]" {
		t.Fatalf("check after peek = %v", got)
	}
	if got := check(); len(got) != 0 {
		t.Fatalf("messages returned twice: %v", got)
	}
	if got := check("--include-own", "--peek"); len(got) != 0 {
		t.Fatalf("cursor should already be past my own message: %v", got)
	}
}

func TestCheckFirstCallShowsRecentContextOnly(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	for i := 1; i <= 6; i++ {
		send(t, address, "writer", "#room", fmt.Sprint("m", i))
	}
	got := checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "#room", "--initial", "3", "--json"))
	if fmt.Sprint(got) != "[m4 m5 m6]" {
		t.Fatalf("first check = %v", got)
	}
}

func TestCheckPagesThroughLargeBacklogs(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	send(t, address, "writer", "#room", "start")
	mustCLI(t, address, "check", "--nick", "me", "--channel", "#room")
	for i := 1; i <= 25; i++ {
		send(t, address, "writer", "#room", fmt.Sprint("n", i))
	}
	got := checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "#room", "--limit", "10", "--json"))
	if len(got) != 25 || got[0] != "n1" || got[24] != "n25" {
		t.Fatalf("paged check returned %d messages: %v", len(got), got)
	}
}

func TestCheckWaitWakesWhenAMessageArrives(t *testing.T) {
	agentEnv(t)
	tap := tapDaemon(t, cliTestServer(t))
	address := tap.addr
	send(t, address, "writer", "#room", "before")
	mustCLI(t, address, "check", "--nick", "me", "--channel", "#room")

	var out string
	var checkErr error
	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		out, _, checkErr = cli(t, address, "check", "--nick", "me", "--channel", "#room", "--wait", "20s", "--json")
	}()
	tap.waitObserving(t, 1)
	send(t, address, "me", "#room", "my own message must not wake me")
	select {
	case <-done:
		t.Fatalf("own message woke the wait: %q %v", out, checkErr)
	case <-time.After(200 * time.Millisecond): // bounded "nothing happens" window
	}
	send(t, address, "writer", "#room", "reply")
	<-done
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	if got := checkBodies(t, out); fmt.Sprint(got) != "[reply]" {
		t.Fatalf("wait returned %v", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("wait took %s; it should return as soon as the reply arrives", elapsed)
	}
	if got := checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "#room", "--json")); len(got) != 0 {
		t.Fatalf("message delivered by wait was returned again: %v", got)
	}
}

func TestCheckWaitTimesOutQuietly(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	start := time.Now()
	out, _, err := cli(t, address, "check", "--nick", "me", "--channel", "#room", "--wait", "400ms")
	if err != nil || out != "" {
		t.Fatalf("quiet wait = %q, %v", out, err)
	}
	if elapsed := time.Since(start); elapsed < 350*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("wait lasted %s", elapsed)
	}
}

func TestCheckWaitReceivesDirectMessages(t *testing.T) {
	agentEnv(t)
	tap := tapDaemon(t, cliTestServer(t))
	address := tap.addr
	done := make(chan string, 1)
	go func() {
		out, _, _ := cli(t, address, "check", "--nick", "me", "--wait", "20s", "--json")
		done <- out
	}()
	tap.waitObserving(t, 1)
	mustCLI(t, address, "send", "--nick", "planner", "--to", "me", "--message", "ping")
	select {
	case out := <-done:
		if got := checkBodies(t, out); fmt.Sprint(got) != "[ping]" {
			t.Fatalf("got %v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("direct message did not wake check --wait")
	}
}

func TestCheckWarnsWhenTheCursorHasExpired(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t) // retains 16 messages
	send(t, address, "writer", "#room", "first")
	mustCLI(t, address, "check", "--nick", "me", "--channel", "#room")
	for i := 0; i < 20; i++ {
		send(t, address, "writer", "#room", fmt.Sprint("flood", i))
	}
	stdout, stderr, err := cli(t, address, "check", "--nick", "me", "--channel", "#room", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "may have been missed") {
		t.Fatalf("no expiry warning on stderr: %q", stderr)
	}
	got := checkBodies(t, stdout)
	if len(got) == 0 || got[len(got)-1] != "flood19" {
		t.Fatalf("expected the latest messages, got %v", got)
	}
	if out := mustCLI(t, address, "check", "--nick", "me", "--channel", "#room", "--json"); len(checkBodies(t, out)) != 0 || checkFooter(t, out).Code != "no_messages" {
		t.Fatalf("cursor was not repaired after expiry: %s", out)
	}
}

func TestCheckRefusesToRunTwiceConcurrentlyForOneNick(t *testing.T) {
	agentEnv(t)
	wait := cursorLockWait
	cursorLockWait = 20 * time.Millisecond
	t.Cleanup(func() { cursorLockWait = wait })
	first, err := openCursors(options{addr: "127.0.0.1:1"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openCursors(options{addr: "127.0.0.1:1"}, "ME"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second lock = %v", err)
	}
	first.close()
	again, err := openCursors(options{addr: "127.0.0.1:1"}, "me")
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	again.close()
}

func TestHistoryAfterPagesServerSideAndReadsDirectMessages(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	for i := 1; i <= 5; i++ {
		send(t, address, "writer", "#room", fmt.Sprint("h", i))
	}
	// Page forward from the oldest message: h2 and h3 follow h1, and more remain.
	var oldest irc.HistoryEvent
	if err := json.Unmarshal([]byte(strings.SplitN(mustCLI(t, address, "history", "#room", "--json"), "\n", 2)[0]), &oldest); err != nil {
		t.Fatal(err)
	}
	cursor := oldest.ID
	stdout, stderr, err := cli(t, address, "history", "#room", "--after", cursor, "--limit", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(stdout), "\n"); len(lines) != 2 || !strings.Contains(lines[0], `"h2"`) {
		t.Fatalf("page = %q", stdout)
	}
	if !strings.Contains(stderr, "--after") {
		t.Fatalf("no continuation hint: %q", stderr)
	}
	mustCLI(t, address, "send", "--nick", "planner", "--to", "Builder", "--message", "dm")
	if out := mustCLI(t, address, "history", "builder"); !strings.Contains(out, "planner: dm") {
		t.Fatalf("inbox history = %q", out)
	}
}

func checkMessages(t *testing.T, output string) []checkMessage {
	t.Helper()
	var out []checkMessage
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var m checkMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad JSON line %q: %v", line, err)
		}
		if m.Type == "message" {
			out = append(out, m)
		}
	}
	return out
}

func TestCheckFlagsMessagesThatTagOrAddressYouFromAnyChannel(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	send(t, address, "writer", "#room", "plain chatter")
	send(t, address, "writer", "#room", "hey @Me look at this")
	send(t, address, "writer", "#elsewhere", "me: you are needed in a channel you do not follow")
	send(t, address, "writer", "#room", "@someone-else not you")
	mustCLI(t, address, "send", "--nick", "writer", "--to", "me", "--message", "direct")

	got := checkMessages(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "#room", "--json"))
	flagged := map[string]bool{}
	for _, m := range got {
		flagged[m.Message] = m.Mentioned
	}
	want := map[string]bool{
		"plain chatter": false, "hey @Me look at this": true, "@someone-else not you": false, "direct": true,
		"me: you are needed in a channel you do not follow": true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d (a tagged message in a followed channel must not appear twice): %#v", len(got), len(want), got)
	}
	for body, mentioned := range want {
		if v, ok := flagged[body]; !ok || v != mentioned {
			t.Errorf("%q: present=%v mentioned=%v, want mentioned=%v", body, ok, v, mentioned)
		}
	}
	human := mustCLI(t, address, "check", "--nick", "other", "--channel", "#room")
	if strings.Contains(human, "(mentions you)") && strings.Contains(human, "plain chatter: ") {
		t.Fatalf("someone else's check flagged messages that are not theirs:\n%s", human)
	}
}

func TestCheckMentionsOnlyIgnoresRoomTrafficAndLeavesRoomCursorsAlone(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	send(t, address, "writer", "#room", "noise one")
	send(t, address, "writer", "#room", "builder: please run the tests")
	send(t, address, "writer", "#other", "noise two")
	send(t, address, "writer", "#other", "ping @builder")

	got := checkMessages(t, mustCLI(t, address, "check", "--nick", "builder", "--mentions", "--json"))
	if len(got) != 2 || got[0].Message != "builder: please run the tests" || got[1].Message != "ping @builder" || !got[0].Mentioned || !got[1].Mentioned {
		t.Fatalf("--mentions returned %#v", got)
	}
	if again := checkMessages(t, mustCLI(t, address, "check", "--nick", "builder", "--mentions", "--json")); len(again) != 0 {
		t.Fatalf("mentions were returned twice: %#v", again)
	}
	// Room messages were never marked read by the mentions-only check.
	room := checkBodies(t, mustCLI(t, address, "check", "--nick", "builder", "--channel", "#room", "--json"))
	if fmt.Sprint(room) != "[noise one builder: please run the tests]" {
		t.Fatalf("room check after --mentions = %v", room)
	}
}

func TestCheckWaitMentionsIgnoresChatterAndWakesOnATag(t *testing.T) {
	agentEnv(t)
	tap := tapDaemon(t, cliTestServer(t))
	address := tap.addr
	done := make(chan string, 1)
	start := time.Now()
	go func() {
		out, _, _ := cli(t, address, "check", "--nick", "sleeper", "--mentions", "--wait", "20s", "--json")
		done <- out
	}()
	tap.waitObserving(t, 1)
	send(t, address, "writer", "#anywhere", "loud unrelated chatter")
	send(t, address, "writer", "#anywhere", "more chatter")
	select {
	case out := <-done:
		t.Fatalf("check --mentions --wait woke up for chatter: %q", out)
	case <-time.After(700 * time.Millisecond):
	}
	send(t, address, "writer", "#anywhere", "wake up @sleeper")
	select {
	case out := <-done:
		got := checkMessages(t, out)
		if len(got) != 1 || got[0].Message != "wake up @sleeper" || !got[0].Mentioned {
			t.Fatalf("woke with %#v", got)
		}
		if time.Since(start) > 8*time.Second {
			t.Fatalf("took %s to wake", time.Since(start))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a tag did not wake check --wait --mentions")
	}
}

func TestTopicCommandSetsReadsAndClears(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	if out := mustCLI(t, address, "topic", "hq"); !strings.Contains(out, "has no topic") { // bare name accepted
		t.Fatalf("empty topic = %q", out)
	}
	out := mustCLI(t, address, "topic", "#hq", "--nick", "planner", "--set", "Welcome! Tag @planner for decisions.")
	if !strings.Contains(out, "set the topic of #hq") {
		t.Fatalf("set = %q", out)
	}
	if got := strings.TrimSpace(mustCLI(t, address, "topic", "#hq")); got != "Welcome! Tag @planner for decisions." {
		t.Fatalf("read = %q", got)
	}
	var topic struct{ Channel, Topic string }
	if err := json.Unmarshal([]byte(mustCLI(t, address, "topic", "#hq", "--json")), &topic); err != nil || topic.Topic == "" || topic.Channel != "#hq" {
		t.Fatalf("json = %+v %v", topic, err)
	}
	if _, _, err := cli(t, address, "topic", "#hq", "--set", strings.Repeat("x", 500), "--nick", "planner"); err == nil || !strings.Contains(err.Error(), "400 bytes") {
		t.Fatalf("an oversized topic should be refused clearly, got %v", err)
	}
	if _, _, err := cli(t, address, "topic", "#hq", "--set", "x", "--clear", "--nick", "planner"); err == nil {
		t.Fatal("--set and --clear together should be rejected")
	}
	if out := mustCLI(t, address, "topic", "#hq", "--clear", "--nick", "planner"); !strings.Contains(out, "cleared") {
		t.Fatalf("clear = %q", out)
	}
	if out := mustCLI(t, address, "topic", "#hq"); !strings.Contains(out, "has no topic") {
		t.Fatalf("after clear = %q", out)
	}
}

func TestCheckShowsAChannelHeaderOnceAndAgainWhenItChanges(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "topic", "#room", "--nick", "planner", "--set", "Rules: one thread per task")
	send(t, address, "writer", "#room", "hello")
	check := func(extra ...string) string {
		return mustCLI(t, address, append([]string{"check", "--nick", "me", "--channel", "#room"}, extra...)...)
	}
	first := check()
	if !strings.HasPrefix(first, "#room topic: Rules: one thread per task\n") || !strings.Contains(first, "writer [") || !strings.Contains(first, "]: hello") {
		t.Fatalf("the first check should lead with the header:\n%s", first)
	}
	if again := check(); strings.Contains(again, "topic") {
		t.Fatalf("an unchanged header was shown again:\n%s", again)
	}
	mustCLI(t, address, "topic", "#room", "--nick", "planner", "--set", "Rules: updated")
	if peek := check("--peek"); !strings.Contains(peek, "topic: Rules: updated") {
		t.Fatalf("a changed header should be shown:\n%s", peek)
	}
	if again := check(); !strings.Contains(again, "topic: Rules: updated") {
		t.Fatalf("--peek must not mark the header as seen:\n%s", again)
	}
	if done := check(); strings.Contains(done, "topic") {
		t.Fatalf("header repeated after being seen:\n%s", done)
	}
	mustCLI(t, address, "topic", "#room", "--nick", "planner", "--set", "Third header")
	lines := strings.Split(strings.TrimSpace(check("--json")), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], `"type":"status"`) || !strings.Contains(lines[0], `"type":"topic"`) || !strings.Contains(lines[0], `"topic":"Third header"`) {
		t.Fatalf("json header record = %v", lines)
	}
	mustCLI(t, address, "topic", "#room", "--nick", "planner", "--clear")
	if cleared := check(); strings.Contains(cleared, "topic") {
		t.Fatalf("clearing a header should not print anything: %q", cleared)
	}
}

func TestWatchShowsHeadersAtStartAndLiveChanges(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "topic", "#room", "--nick", "planner", "--set", "Standup at noon")
	out, _ := startWatch(t, "--channel", "#room", "--color", "never", "--backlog", "0", "--addr", address)
	waitForOutput(t, out, "Topic of #room: Standup at noon")
	mustCLI(t, address, "topic", "#room", "--nick", "dana", "--set", "Moved to 1pm")
	waitForOutput(t, out, "dana set the topic of #room: Moved to 1pm")
	mustCLI(t, address, "topic", "#room", "--nick", "dana", "--clear")
	waitForOutput(t, out, "dana cleared the topic of #room")
}
