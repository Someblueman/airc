package main

import (
	"context"
	"github.com/Someblueman/airc/pkg/irc"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (h *uiHarness) command(text string) {
	h.t.Helper()
	h.model.input = []rune(text)
	cmds, _ := h.model.submit()
	for _, cmd := range cmds {
		h.cmds <- cmd
	}
}

func TestUIThreadsSearchPinsCorrectionsAndLiveSignals(t *testing.T) {
	address := chatServer(t, 64)
	root := posted(t, address, "writer", "#room", "original searchable")
	posted(t, address, "writer", "#alpha", "another room")
	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected && strings.Contains(h.texts("#room"), "original") })
	h.command("/thread last")
	h.until("thread", func() bool {
		return h.model.current == "thread:"+root.ID && strings.Contains(h.texts(h.model.current), "original")
	})
	h.command("answer from thread")
	h.until("thread reply", func() bool { return strings.Contains(h.texts("thread:"+root.ID), "answer from thread") })
	mustCLI(t, address, "prepare", root.ID, "--nick", "writer", "--eta", "5s", "--message", "checking details")
	h.until("signal", func() bool { s, _ := h.model.activeStatus(); return strings.Contains(s, "checking details") })
	mustCLI(t, address, "prepare", root.ID, "--nick", "writer", "--cancel")
	h.until("cancel", func() bool { s, _ := h.model.activeStatus(); return !strings.Contains(s, "checking details") })
	h.command("/react last 🎉")
	h.until("reaction", func() bool { return strings.Contains(h.texts("#room"), "🎉") })
	h.command("/pin " + root.ID)
	h.until("pin", func() bool { s, _ := h.model.activeStatus(); return strings.HasPrefix(s, "pin ") })
	mustCLI(t, address, "correct", root.ID, "--nick", "writer", "--message", "accurate correction")
	h.until("correction", func() bool {
		for _, e := range h.model.find("#room").items {
			if m, ok := e.(*irc.MessageEvent); ok && m.ID == root.ID {
				return strings.Contains(chatBody(m.ChatMetadata, m.ID, m.From, m.Message), "superseded")
			}
		}
		return false
	})
	h.command("/close")
	if h.model.current != "#room" {
		t.Fatal("query close lost source room", h.model.current)
	}
	h.command("/search searchable")
	h.until("search", func() bool {
		return h.model.current == "search:#room" && strings.Contains(h.texts("search:#room"), "original")
	})
}

func TestDesktopNotificationsOptInQuietHoursThrottleAndCleanText(t *testing.T) {
	n, err := newDesktopNotifier(false, "22:00-08:00")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	n.now = func() time.Time { return now }
	calls := 0
	n.send = func(ctx context.Context, title, body string) error {
		calls++
		if strings.ContainsAny(title+body, "\x1b\n") {
			t.Fatal("unsafe notification")
		}
		if len([]rune(body)) > 161 {
			t.Fatal("unbounded notification")
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("unbounded subprocess")
		}
		return nil
	}
	send := func() {
		t.Helper()
		if err := n.notify(context.Background(), "AIRC\x1b", strings.Repeat("🙂", 200)+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	send()
	if calls != 0 {
		t.Fatal("enabled by default")
	}
	n.enabled = true
	now = time.Date(2026, 10, 1, 23, 0, 0, 0, time.Local)
	send()
	now = now.Add(8 * time.Hour)
	send()
	if calls != 0 {
		t.Fatal("quiet hours ignored")
	}
	now = now.Add(time.Hour)
	send()
	send()
	if calls != 1 {
		t.Fatal("throttle or quiet boundary")
	}
	now = now.Add(10 * time.Second)
	send()
	if calls != 2 {
		t.Fatal("throttle never released")
	}
	if _, err := newDesktopNotifier(true, "nonsense"); err == nil {
		t.Fatal("invalid quiet hours accepted")
	}
}

func TestUIModerationUsesCredentialAndEnforcesRules(t *testing.T) {
	address := chatServer(t, 32)
	path := filepath.Join(t.TempDir(), "admin.token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIRC_ADMIN_TOKEN_FILE", path)
	posted(t, address, "writer", "#room", "seed")
	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected })
	h.command("/mute writer repeated output")
	h.until("mute", func() bool { s, _ := h.model.activeStatus(); return strings.Contains(s, "mute writer") })
	deniedCLI(t, address, "Muted", "send", "--nick", "writer", "--channel", "room", "--message", "blocked")
	h.command("/unmute writer")
	h.until("unmute", func() bool { s, _ := h.model.activeStatus(); return strings.HasPrefix(s, "unmute writer") })
	posted(t, address, "writer", "#room", "allowed")
	h.command("/ban writer return later")
	h.until("ban", func() bool { s, _ := h.model.activeStatus(); return strings.HasPrefix(s, "ban writer") })
	deniedCLI(t, address, "banned", "send", "--nick", "writer", "--channel", "room", "--message", "blocked")
	h.command("/unban writer")
	h.until("unban", func() bool { s, _ := h.model.activeStatus(); return strings.HasPrefix(s, "unban writer") })
	posted(t, address, "writer", "#room", "returned")
}
