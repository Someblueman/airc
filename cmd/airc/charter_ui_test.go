package main

import (
	"strings"
	"testing"
)

// /charter opens the room's charter as a view and /close returns; a change
// made elsewhere is noted in the status line.
func TestUICharterViewAndChangeNotice(t *testing.T) {
	address, token := chatServerWithToken(t, 64)
	send(t, address, "writer", "#room", "hello")
	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected && strings.Contains(h.texts("#room"), "hello") })
	h.command("/charter")
	h.until("no charter", func() bool { s, _ := h.model.activeStatus(); return s == "#room has no charter" })
	var stdout, stderr strings.Builder
	if err := run([]string{"room", "#room", "--charter", "-", "--token-file", token, "--addr", address}, strings.NewReader("Be kind.\nOne thread per task.\n"), &stdout, &stderr); err != nil {
		t.Fatalf("set charter: %v %s", err, stderr.String())
	}
	h.until("change notice", func() bool {
		s, _ := h.model.activeStatus()
		return strings.Contains(s, "changed the charter of #room; /charter shows it")
	})
	h.command("/charter")
	h.until("charter view", func() bool {
		b := h.model.find("charter:#room")
		return h.model.current == "charter:#room" && b != nil && strings.Contains(helpText(b.help), "Charter of #room\nBe kind.\nOne thread per task.")
	})
	h.command("/close")
	if h.model.current != "#room" {
		t.Fatal("closing the charter view did not return to the room", h.model.current)
	}
}

// setCharter sets room's charter as the admin, as an operator would from
// another terminal.
func setCharter(t *testing.T, address, token, room, text string) {
	t.Helper()
	var stdout, stderr strings.Builder
	if err := run([]string{"room", room, "--charter", "-", "--token-file", token, "--addr", address}, strings.NewReader(text), &stdout, &stderr); err != nil {
		t.Fatalf("set charter: %v %s", err, stderr.String())
	}
}

// press feeds one key to the model and runs what it asks of the backend.
func (h *uiHarness) press(k key) {
	h.t.Helper()
	cmds, _ := h.model.update(keyIn(k))
	for _, cmd := range cmds {
		h.cmds <- cmd
	}
}

// A charter set after the UI connected, on a room it was not observing,
// sends it no signal; joining the room reads the charter, notes it, and
// /charter shows it.
func TestUICharterReadOnJoinWithoutASignal(t *testing.T) {
	address, token := chatServerWithToken(t, 64)
	send(t, address, "writer", "#room", "hello")
	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected && strings.Contains(h.texts("#room"), "hello") })
	setCharter(t, address, token, "#rules", "Be kind.\nOne thread per task.\n")
	h.command("/join #rules")
	h.until("the charter noted on joining", func() bool {
		s, _ := h.model.activeStatus()
		return h.model.current == "#rules" && s == "#rules has a charter; /charter shows it"
	})
	h.command("/charter")
	h.until("charter view", func() bool {
		b := h.model.find("charter:#rules")
		return h.model.current == "charter:#rules" && b != nil && strings.Contains(helpText(b.help), "Charter of #rules\nBe kind.\nOne thread per task.")
	})
}

// Switching to a room whose charter the UI has not read reads it, so
// /charter shows a charter set before the UI connected.
func TestUICharterReadOnSwitchingRooms(t *testing.T) {
	address, token := chatServerWithToken(t, 64)
	send(t, address, "writer", "#alpha", "hello")
	send(t, address, "writer", "#rules", "rules here")
	setCharter(t, address, token, "#rules", "Read the pinned plan first.\n")
	h := startUIBackend(t, address, "#alpha", "#rules")
	h.until("startup", func() bool { return h.model.connected && strings.Contains(h.texts("#rules"), "rules here") })
	if h.model.current != "#alpha" || h.model.find("#rules").charterKnown {
		t.Fatalf("current %q; the charter of a room not shown was read", h.model.current)
	}
	h.press(key{kind: keyTab})
	h.until("the charter noted on switching", func() bool {
		s, _ := h.model.activeStatus()
		return h.model.current == "#rules" && s == "#rules has a charter; /charter shows it"
	})
	h.command("/charter")
	h.until("charter view", func() bool {
		b := h.model.find("charter:#rules")
		return h.model.current == "charter:#rules" && b != nil && helpText(b.help) == "Charter of #rules\nRead the pinned plan first."
	})
	h.command("/close")
	if h.model.current != "#rules" {
		t.Fatal("closing the charter view did not return to the room", h.model.current)
	}
	// The room's charter is read once; showing it again does not ask again.
	if cmds := h.model.switchTo("#alpha"); len(cmds) != 1 || cmds[0].kind != "names" {
		t.Fatalf("switching back asked the backend for %v", cmds)
	}
}

// A live change still notes who changed the charter in the status line, and
// forgets what the UI read: showing the room again reads the new charter.
func TestUICharterChangeSignalNotesAndRereads(t *testing.T) {
	address, token := chatServerWithToken(t, 64)
	send(t, address, "writer", "#room", "hello")
	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected && h.model.find("#room").charterKnown })
	setCharter(t, address, token, "#room", "First rules.\n")
	h.until("change notice", func() bool {
		s, _ := h.model.activeStatus()
		return strings.Contains(s, "changed the charter of #room; /charter shows it")
	})
	if b := h.model.find("#room"); b.charterKnown || b.charterAsked {
		t.Fatal("the signal did not forget the charter the UI read")
	}
	h.press(key{kind: keyTab})
	h.press(key{kind: keyBackTab})
	h.until("the new charter read on showing the room again", func() bool {
		s, _ := h.model.activeStatus()
		return h.model.current == "#room" && s == "#room has a charter; /charter shows it" && h.model.find("#room").charter == "First rules."
	})
}

// The model reads the open room's charter when the connection comes up and
// again after a reconnect, notes a change made while it was away, redraws an
// open view in place, and says when the daemon predates charters.
func TestUIModelReadsTheCharterOnConnectAndReconnect(t *testing.T) {
	m := newTestModel("#a")
	online, offline := true, false
	charterReads := func(cmds []uiCmd) int {
		n := 0
		for _, cmd := range cmds {
			if cmd.kind == "charter" && cmd.target == "#a" && cmd.text == "" {
				n++
			}
		}
		return n
	}
	if cmds, _ := m.update(statusIn{connected: &online}); charterReads(cmds) != 1 {
		t.Fatalf("connecting asked for %v", cmds)
	}
	if cmds := m.switchTo("#a"); charterReads(cmds) != 0 {
		t.Fatalf("a read already queued was asked for again: %v", cmds)
	}
	m.update(charterIn{channel: "#a", charter: "Be kind."})
	if s, _ := m.activeStatus(); s != "#a has a charter; /charter shows it" {
		t.Fatalf("status %q", s)
	}
	m.update(statusIn{connected: &offline})
	if cmds, _ := m.update(statusIn{connected: &online}); charterReads(cmds) != 1 {
		t.Fatalf("reconnecting asked for %v", cmds)
	}
	m.status = ""
	m.update(charterIn{channel: "#a", charter: "Be kind."})
	if s, _ := m.activeStatus(); s != "" {
		t.Fatalf("an unchanged charter was noted: %q", s)
	}
	m.update(charterIn{channel: "#a", charter: "Be brief."})
	if s, _ := m.activeStatus(); s != "the charter of #a changed while the UI was away; /charter shows it" {
		t.Fatalf("status %q", s)
	}

	// /charter opens the view; a later read redraws it where it is.
	m.update(charterIn{channel: "#a", charter: "Be brief.", open: true})
	view := m.find("charter:#a")
	if m.current != "charter:#a" || view == nil || helpText(view.help) != "Charter of #a\nBe brief." {
		t.Fatalf("current %q view %v", m.current, view)
	}
	m.update(charterIn{channel: "#a", charter: "Be brief.\nBe kind."})
	if m.current != "charter:#a" || helpText(view.help) != "Charter of #a\nBe brief.\nBe kind." {
		t.Fatalf("current %q view\n%s", m.current, helpText(view.help))
	}
	m.switchTo("#a")
	m.update(charterIn{channel: "#a", open: true})
	if s, _ := m.activeStatus(); s != "#a has no charter" || m.current != "#a" || helpText(view.help) != "Charter of #a\n#a has no charter" {
		t.Fatalf("a cleared charter: status %q current %q view\n%s", s, m.current, helpText(view.help))
	}

	// A daemon from before charters: the view says so, and a read made in
	// the background notes nothing.
	m.update(charterIn{channel: "#a", open: true, unsupported: true})
	if m.current != "charter:#a" || helpText(view.help) != "Charter of #a\n"+errNoCharters.Error() {
		t.Fatalf("current %q view\n%s", m.current, helpText(view.help))
	}
	m.status = ""
	m.update(charterIn{channel: "#a", unsupported: true})
	if s, _ := m.activeStatus(); s != "" {
		t.Fatalf("status %q", s)
	}
}
