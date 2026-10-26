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
