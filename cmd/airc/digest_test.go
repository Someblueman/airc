package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
)

func digestJSON(t *testing.T, address string, args ...string) digestSummary {
	t.Helper()
	var d digestSummary
	out := mustCLI(t, address, append([]string{"digest", "--nick", "planner", "--channel", "build", "--json"}, args...)...)
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("digest output %q: %v", out, err)
	}
	return d
}

func TestDigestSummarizesUnreadWithoutBodiesAndMarksNothingRead(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 200})
	mustCLI(t, address, "topic", "#build", "--set", "Builds and releases", "--nick", "bob")
	for _, body := range []string{"build 41 is green", "build 42 is red", "retrying build 42"} {
		send(t, address, "bob", "#build", body)
	}
	root := send(t, address, "bob", "#build", "the flaky test is in solver_test.go "+strings.Repeat("and a lot more detail ", 20))
	replyMessage(t, address, "carol", root.ID, "I can take the flaky test")
	replyMessage(t, address, "dave", root.ID, "it is the timeout")
	wrong := send(t, address, "carol", "#build", "release is on Friday")
	mustCLI(t, address, "correct", wrong.ID, "--nick", "carol", "--message", "release is on Thursday")
	mustCLI(t, address, "poll", "--nick", "carol", "--channel", "build", "--question", "Ship 42?", "--option", "yes", "--option", "no", "--for", "10m")
	review := send(t, address, "carol", "#build", "@planner can you review the release notes?")
	port := send(t, address, "dave", "#build", "@planner which port do we use?")
	replyMessage(t, address, "planner", port.ID, "6667")
	hello := sentMessage(t, address, "--nick", "erin", "--to", "planner", "--message", "hello, a question about the deploy")
	replyMessage(t, address, "planner", hello.ID, "answered in the DM")
	urgent := sentMessage(t, address, "--nick", "frank", "--to", "planner", "--message", "need you on the outage\nsecond line")

	before := mustCLI(t, address, "unread", "--nick", "planner", "--channel", "build", "--json")
	d := digestJSON(t, address)
	if d.Type != "digest" || d.MarkedRead || d.More {
		t.Fatalf("digest header = %+v", d)
	}
	if d.Unread != 13 || d.Addressed != 4 {
		t.Fatalf("unread %d addressed %d, want 13 and 4 (own replies excluded)", d.Unread, d.Addressed)
	}
	if len(d.Rooms) != 2 || d.Rooms[0].Target != "#build" || d.Rooms[1].Target != "inbox" {
		t.Fatalf("rooms = %+v", d.Rooms)
	}
	build := d.Rooms[0]
	if build.Unread != 11 || build.Mentions != 2 || build.Speakers[0].Nick != "carol" || build.Speakers[0].Messages != 5 || build.Speakers[1].Nick != "bob" {
		t.Fatalf("#build = %+v", build)
	}
	if len(build.Threads) != 1 || build.Threads[0].ID != root.ID || build.Threads[0].Messages != 3 || strings.Join(build.Threads[0].Participants, ",") != "bob,carol,dave" {
		t.Fatalf("threads = %+v", build.Threads)
	}
	if n := len([]rune(build.Threads[0].Preview)); n > digestPreview {
		t.Fatalf("preview is %d runes", n)
	}
	var waiting []string
	for _, item := range d.Waiting {
		waiting = append(waiting, item.ID)
		if strings.Contains(item.Preview, "\n") {
			t.Fatalf("multi-line preview: %q", item.Preview)
		}
	}
	if strings.Join(waiting, ",") != review.ID+","+urgent.ID {
		t.Fatalf("waiting on you = %+v; the port and hello were answered", d.Waiting)
	}
	if len(d.Corrections) != 1 || d.Corrections[0].Supersedes != wrong.ID || d.Corrections[0].Kind != "correct" {
		t.Fatalf("corrections = %+v", d.Corrections)
	}
	if len(d.Polls) != 1 || d.Polls[0].Preview != "Ship 42?" || strings.Join(d.Polls[0].Options, ",") != "yes,no" || !d.Polls[0].ClosesAt.After(time.Now()) {
		t.Fatalf("polls = %+v", d.Polls)
	}
	if len(d.Headers) == 0 || d.Headers[0].Type != "topic" || d.Headers[0].Preview != "Builds and releases" {
		t.Fatalf("headers = %+v", d.Headers)
	}

	text := mustCLI(t, address, "digest", "--nick", "planner", "--channel", "build")
	for _, want := range []string{"13 unread in 2 places, 4 addressed to you (2 waiting on you)", "#build: 11 unread, 2 for you", "thread " + root.ID + ": 3 messages from bob, carol, dave", "Waiting on you:", "DM frank: \"need you on the outage second line\"", "carol corrected " + wrong.ID, "Open polls:", "Nothing was marked read"} {
		if !strings.Contains(text, want) {
			t.Errorf("text digest lacks %q:\n%s", want, text)
		}
	}
	if after := mustCLI(t, address, "unread", "--nick", "planner", "--channel", "build", "--json"); after != before {
		t.Fatalf("a digest without --mark-read changed what is unread:\n%s\n%s", before, after)
	}

	if d = digestJSON(t, address, "--mark-read"); !d.MarkedRead || d.Unread != 13 {
		t.Fatalf("mark-read digest = %+v", d)
	}
	if out := mustCLI(t, address, "unread", "--nick", "planner", "--channel", "build"); out != "" {
		t.Fatalf("still unread after --mark-read: %s", out)
	}
	if out := mustCLI(t, address, "digest", "--nick", "planner", "--channel", "build"); out != "" {
		t.Fatalf("an empty digest should print nothing: %s", out)
	}
	if d = digestJSON(t, address); d.Unread != 0 || len(d.Rooms) != 0 || len(d.Waiting) != 0 {
		t.Fatalf("empty digest = %+v", d)
	}
}

func TestDigestListsUncertainSendsWithoutTouchingThem(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 200})
	send(t, address, "bob", "#build", "hello planner")
	// A send that never got its receipt: the intent is in the outbox, the
	// message is not on the server.
	box, err := openOutbox(options{addr: address, nick: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := box.add("#build", "", "did this land?\nsecond line", "lost-request"); err != nil {
		t.Fatal(err)
	}
	box.close()

	before := mustCLI(t, address, "unread", "--nick", "planner", "--channel", "build", "--json")
	d := digestJSON(t, address)
	if len(d.PendingSends) != 1 || d.PendingMore != 0 {
		t.Fatalf("pending sends = %+v", d.PendingSends)
	}
	if p := d.PendingSends[0]; p.RequestID != "lost-request" || p.Target != "#build" || p.Preview != "did this land? second line" || p.Created.IsZero() || time.Since(p.Created) > time.Minute {
		t.Fatalf("pending send = %+v", p)
	}
	text := mustCLI(t, address, "digest", "--nick", "planner", "--channel", "build")
	if want := "1 uncertain send; airc send --pending lists it; --retry REQUEST_ID recovers a receipt"; !strings.Contains(text, want) {
		t.Fatalf("text digest lacks %q:\n%s", want, text)
	}
	// The digest read the outbox as it reads everything: nothing marked
	// read, nothing posted, the entry still there for send --pending.
	if after := mustCLI(t, address, "unread", "--nick", "planner", "--channel", "build", "--json"); after != before {
		t.Fatalf("a digest changed what is unread:\n%s\n%s", before, after)
	}
	if history := mustCLI(t, address, "history", "#build", "--json"); strings.Contains(history, "did this land") {
		t.Fatal("a digest posted an uncertain send")
	}
	if pending := mustCLI(t, address, "send", "--nick", "planner", "--pending", "--json"); !strings.Contains(pending, "lost-request") {
		t.Fatal(pending)
	}
	// Another nickname's digest does not list it; this one's does even
	// with nothing unread, since it is the one thing to settle first.
	if other := cliJSON[digestSummary](t, address, "digest", "--nick", "bob", "--channel", "build"); len(other.PendingSends) != 0 {
		t.Fatalf("bob's digest lists planner's sends: %+v", other.PendingSends)
	}
	mustCLI(t, address, "digest", "--nick", "planner", "--channel", "build", "--mark-read")
	if text := mustCLI(t, address, "digest", "--nick", "planner", "--channel", "build"); !strings.Contains(text, "1 uncertain send") {
		t.Fatalf("an otherwise empty digest hid the uncertain send:\n%s", text)
	}
	mustCLI(t, address, "send", "--nick", "planner", "--forget", "lost-request")
	if out := mustCLI(t, address, "digest", "--nick", "planner", "--channel", "build"); out != "" {
		t.Fatalf("a settled outbox still printed: %s", out)
	}
}

func TestDigestBoundsWhatItLists(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 200})
	for i := range digestWaiting + 5 {
		send(t, address, "bob", "#build", "@planner question "+strings.Repeat("x", i))
	}
	for _, nick := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"} {
		send(t, address, nick, "#build", "hello")
	}
	d := digestJSON(t, address)
	if len(d.Waiting) != digestWaiting || d.WaitingMore != 5 {
		t.Fatalf("waiting %d omitted %d", len(d.Waiting), d.WaitingMore)
	}
	if room := d.Rooms[0]; len(room.Speakers) != digestSpeakers || room.Others != 3 || room.Speakers[0].Nick != "bob" {
		t.Fatalf("speakers = %+v others %d", room.Speakers, room.Others)
	}
	data, _ := json.Marshal(d)
	if len(data) > 8192 {
		t.Fatalf("a digest of %d messages is %d bytes", d.Unread, len(data))
	}
}
