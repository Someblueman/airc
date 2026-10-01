package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

type uiHarness struct {
	t      *testing.T
	model  *uiModel
	msgs   chan any
	cmds   chan uiCmd
	cancel context.CancelFunc
}

func startUIBackend(t *testing.T, address string, initial ...string) *uiHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := &uiHarness{t: t, model: newUIModel("me", initial, time.Now), msgs: make(chan any, 512), cmds: make(chan uiCmd, 32), cancel: cancel}
	opt := options{addr: address, nick: "me"}
	backend := &uiBackend{opt: opt, nick: "me", initial: initial, backlog: 100, out: h.msgs, cmds: h.cmds}
	done := make(chan struct{})
	go func() { backend.run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("UI backend did not stop")
		}
	})
	return h
}

// until feeds backend messages to the model until the condition holds.
func (h *uiHarness) until(what string, condition func() bool) {
	h.t.Helper()
	deadline := time.After(10 * time.Second)
	for !condition() {
		select {
		case msg := <-h.msgs:
			if fatal, ok := msg.(fatalIn); ok {
				h.t.Fatalf("backend failed while waiting for %s: %v", what, fatal.err)
			}
			for _, cmd := range func() []uiCmd { c, _ := h.model.update(msg); return c }() {
				h.cmds <- cmd
			}
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (h *uiHarness) texts(channel string) string {
	var out []string
	if b := h.model.find(channel); b != nil {
		for _, event := range b.items {
			switch e := event.(type) {
			case *irc.MessageEvent:
				out = append(out, e.From+": "+e.Message)
			case *irc.TopicEvent:
				out = append(out, "topic: "+e.Topic)
			}
		}
	}
	return strings.Join(out, "\n")
}

func TestUIBackendDiscoversChannelsHistoryHeadersAndInbox(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	send(t, address, "planner", "#alpha", "alpha one")
	send(t, address, "planner", "#beta", "beta one")
	send(t, address, "planner", "#beta", "beta two, tagging @me")
	mustCLI(t, address, "send", "--nick", "planner", "--to", "me", "--message", "a direct message")
	mustCLI(t, address, "topic", "#alpha", "--nick", "planner", "--set", "Alpha welcome")
	mustCLI(t, address, "topic", "#gamma", "--nick", "planner", "--set", "A channel with only a header")

	h := startUIBackend(t, address)
	h.until("history and headers", func() bool {
		return h.model.connected && h.model.find("#gamma") != nil && strings.Contains(h.texts("#beta"), "beta two") && strings.Contains(h.texts("@me"), "a direct message")
	})
	var names []string
	for _, b := range h.model.buffers {
		names = append(names, b.name)
	}
	if strings.Join(names, " ") != "#alpha #beta #gamma @me @*" {
		t.Fatalf("every channel on the server should be listed, got %v", names)
	}
	if h.model.find("#alpha").topic != "Alpha welcome" || h.model.find("#gamma").topic != "A channel with only a header" {
		t.Fatalf("headers not loaded: %q %q", h.model.find("#alpha").topic, h.model.find("#gamma").topic)
	}
	for _, b := range h.model.buffers {
		if b.unread != 0 {
			t.Errorf("history must not count as unread: %s has %d", b.name, b.unread)
		}
	}
	inbox := h.texts("@me")
	if !strings.Contains(inbox, "beta two, tagging @me") || !strings.Contains(inbox, "a direct message") || strings.Contains(inbox, "alpha one") {
		t.Fatalf("the inbox should hold exactly the tag and the direct message:\n%s", inbox)
	}
}

func TestUIBackendStreamsLiveTrafficAndExecutesCommands(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	send(t, address, "planner", "#alpha", "seed")
	send(t, address, "planner", "#beta", "seed")
	h := startUIBackend(t, address)
	h.until("startup", func() bool { return h.model.connected && h.model.find("#beta") != nil && h.texts("#beta") != "" })

	// Live traffic in a channel that is not open raises its unread count.
	send(t, address, "planner", "#beta", "live one")
	send(t, address, "planner", "#beta", "live two for @me")
	h.until("live messages", func() bool { return h.model.find("#beta").unread == 2 })
	if beta := h.model.find("#beta"); !beta.mention || h.model.inbox().unread != 1 {
		t.Fatalf("mention=%v inbox unread=%d", beta.mention, h.model.inbox().unread)
	}

	// Sending from the UI shows up as the user's own message without raising unread.
	h.cmds <- uiCmd{kind: "send", target: "#alpha", text: "hello from the ui"}
	h.until("own message", func() bool { return strings.Contains(h.texts("#alpha"), "me: hello from the ui") })
	if h.model.find("#alpha").unread != 0 {
		t.Error("the user's own message must not count as unread")
	}

	// Setting a header updates the model and is announced in the channel.
	h.cmds <- uiCmd{kind: "topic", target: "#alpha", text: "Set from the UI"}
	h.until("topic", func() bool { return h.model.find("#alpha").topic == "Set from the UI" })
	h.until("topic announcement", func() bool { return strings.Contains(h.texts("#alpha"), "topic: Set from the UI") })

	// A direct message to someone offline is queued and reported.
	h.cmds <- uiCmd{kind: "send", target: "bob", text: "private note"}
	h.until("queued direct message", func() bool {
		status, _ := h.model.activeStatus()
		return strings.Contains(status, "queued") && strings.Contains(h.texts("@me"), "me: private note")
	})

	// Connected sessions appear in the member list once names are requested.
	member, err := irc.Dial(irc.Config{Nick: "anvil", Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	defer member.Close()
	if err := member.Join("#alpha"); err != nil {
		t.Fatal(err)
	}
	// NAMES uses a different connection; wait for JOIN to finish on the server.
	joined := false
	deadline := time.After(3 * time.Second)
	for !joined {
		select {
		case event := <-member.Events():
			if raw, ok := event.(*irc.RawEvent); ok && raw.Command == "366" {
				joined = true
			}
		case <-deadline:
			t.Fatal("member JOIN was not acknowledged")
		}
	}
	h.cmds <- uiCmd{kind: "names", target: "#alpha"}
	h.until("member list", func() bool {
		for _, nick := range h.model.find("#alpha").live {
			if nick == "anvil" {
				return true
			}
		}
		return false
	})

	// A channel created after startup is discovered and subscribed to.
	send(t, address, "planner", "#fresh", "first message in a new channel")
	h.cmds <- uiCmd{kind: "channels"}
	h.until("a new channel", func() bool {
		return h.model.find("#fresh") != nil && strings.Contains(h.texts("#fresh"), "first message")
	})
	send(t, address, "planner", "#fresh", "and a live one")
	h.until("traffic in the new channel", func() bool { return strings.Contains(h.texts("#fresh"), "and a live one") })
}

func TestUIBackendReconnectsAndCatchesUp(t *testing.T) {
	agentEnv(t)
	old := watchMinBackoff
	watchMinBackoff = 300 * time.Millisecond
	t.Cleanup(func() { watchMinBackoff = old })
	file := filepath.Join(t.TempDir(), "history.jsonl")
	cfg := server.Config{HistoryLimit: 64}

	first, address := startServerAt(t, "127.0.0.1:0", cfg, file)
	send(t, address, "planner", "#alpha", "before the outage")
	h := startUIBackend(t, address)
	h.until("startup", func() bool { return h.model.connected && strings.Contains(h.texts("#alpha"), "before the outage") })
	h.model.update(keyIn{kind: keyTab}) // look at the inbox, so #alpha accumulates unread

	first.stop(t)
	h.until("the outage to be noticed", func() bool { return !h.model.connected })
	_, _ = startServerAt(t, address, cfg, file)
	send(t, address, "planner", "#alpha", "during the outage")
	h.until("catch-up after reconnecting", func() bool {
		return h.model.connected && strings.Contains(h.texts("#alpha"), "during the outage")
	})
	if got := strings.Count(h.texts("#alpha"), "before the outage"); got != 1 {
		t.Errorf("history was repeated %d times after reconnecting", got)
	}
	if alpha := h.model.find("#alpha"); alpha.unread != 1 {
		t.Errorf("a message missed during the outage should be unread, got %d", alpha.unread)
	}
	send(t, address, "planner", "#alpha", "after the outage")
	h.until("live traffic after reconnecting", func() bool { return strings.Contains(h.texts("#alpha"), "after the outage") })
}

func TestUIBackendReportsAServerWithoutChannelListing(t *testing.T) {
	// A server that does not advertise CHANNELS cannot drive the UI; it must say so.
	_, err := (&uiBackend{}).session(context.Background(), &irc.Client{}, true)
	if err == nil || !strings.Contains(err.Error(), "current aircd") {
		t.Fatalf("session error = %v", err)
	}
}
