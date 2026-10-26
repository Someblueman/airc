package main

import (
	"strings"
	"testing"
)

// /run ID opens a view of the room's messages posted under that label.
func TestUIRunViewFiltersTheRoom(t *testing.T) {
	address := chatServer(t, 64)
	sentMessage(t, address, "--nick", "writer", "--channel", "room", "--message", "labelled one", "--run", "run-7")
	sentMessage(t, address, "--nick", "writer", "--channel", "room", "--message", "unlabelled")
	sentMessage(t, address, "--nick", "writer", "--channel", "room", "--message", "labelled two", "--run", "run-7")
	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected && strings.Contains(h.texts("#room"), "labelled two") })
	h.command("/run run-7")
	h.until("run view", func() bool {
		text := h.texts("run:#room")
		return h.model.current == "run:#room" && strings.Contains(text, "labelled one") && strings.Contains(text, "labelled two") && !strings.Contains(text, "unlabelled")
	})
	h.command("/close")
	if h.model.current != "#room" {
		t.Fatal("closing the run view did not return to the room", h.model.current)
	}
	h.command("/run bad/label")
	if s, isError := h.model.activeStatus(); !isError || !strings.Contains(s, "/run ID") {
		t.Fatalf("a bad label gave %q", s)
	}
}
