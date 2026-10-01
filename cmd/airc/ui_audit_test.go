package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestUIAuditShowsAllDMsWithoutPollutingPersonalInbox(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	mustCLI(t, address, "send", "--nick", "planner", "--to", "muse", "--message", "retained assignment")
	h := startUIBackend(t, address)
	h.until("audit backlog", func() bool {
		return h.model.connected && strings.Contains(h.texts(irc.AllDirectMessages), "retained assignment")
	})
	if h.texts("@me") != "" {
		t.Fatal("someone else's DM was filed in the personal inbox")
	}
	mustCLI(t, address, "send", "--nick", "muse", "--to", "planner", "--message", "private response")
	mustCLI(t, address, "send", "--nick", "planner", "--to", "me", "--message", "personal note")
	h.until("live audit", func() bool {
		return strings.Contains(h.texts(irc.AllDirectMessages), "personal note") && strings.Contains(h.texts("@me"), "personal note")
	})
	if got := h.texts(irc.AllDirectMessages); strings.Count(got, "personal note") != 1 || !strings.Contains(got, "private response") {
		t.Fatalf("audit missed or duplicated messages: %s", got)
	}
	if got := h.texts("@me"); strings.Contains(got, "private response") {
		t.Fatalf("audit polluted the personal inbox: %s", got)
	}
	h.model.switchTo(irc.AllDirectMessages)
	view := ansiPattern.ReplaceAllString(strings.Join(h.model.view(), "\n"), "")
	if !strings.Contains(view, "All DMs") || !strings.Contains(view, "human oversight") || !strings.Contains(view, "[dm -> planner]") {
		t.Fatalf("audit view lacks its label or DM recipient: %s", view)
	}
	if cmds, _ := typeText(h.model, "accidental broadcast"); len(cmds) != 0 {
		t.Fatal("audit view accepted a message without an explicit recipient")
	}
}

func TestUIAuditReconnectsAfterAnInitiallyEmptyDMHistory(t *testing.T) {
	agentEnv(t)
	old := watchMinBackoff
	watchMinBackoff = 300 * time.Millisecond
	t.Cleanup(func() { watchMinBackoff = old })
	file := filepath.Join(t.TempDir(), "history.jsonl")
	cfg := server.Config{HistoryLimit: 64}
	first, address := startServerAt(t, "127.0.0.1:0", cfg, file)
	send(t, address, "planner", "#ops", "anchor")
	h := startUIBackend(t, address)
	h.until("startup", func() bool { return h.model.connected })
	first.stop(t)
	h.until("outage", func() bool { return !h.model.connected })
	_, _ = startServerAt(t, address, cfg, file)
	mustCLI(t, address, "send", "--nick", "planner", "--to", "muse", "--message", "assignment during outage")
	h.until("audit catch-up", func() bool {
		return h.model.connected && strings.Contains(h.texts(irc.AllDirectMessages), "assignment during outage")
	})
	if got := h.texts(irc.AllDirectMessages); strings.Count(got, "assignment during outage") != 1 {
		t.Fatalf("audit catch-up duplicated messages: %s", got)
	}
}

func TestUIAuditReportsUnsupportedDaemon(t *testing.T) {
	m := newTestModel()
	m.update(auditIn{available: false})
	m.switchTo(irc.AllDirectMessages)
	if header := ansiPattern.ReplaceAllString(m.header(m.cur(), 140), ""); !strings.Contains(header, "unavailable") || !strings.Contains(header, "DM_AUDIT") {
		t.Fatalf("unsupported audit was silently presented as empty: %s", header)
	}
}

func TestAuditCodeLinesFitNarrowPanes(t *testing.T) {
	m := newTestModel()
	m.update(msgIn{event: uiMessage("code", "longsendernickname", "longrecipientnickname", "```python\n    value  =  'keep  spaces'\n```", 0)})
	for _, width := range []int{24, 30, 36, 50, 80} {
		for _, line := range m.logLines(m.find(irc.AllDirectMessages), width) {
			if got := visibleWidth(line); got > width {
				t.Fatalf("audit at width %d rendered %d columns: %q", width, got, line)
			}
		}
	}
}
