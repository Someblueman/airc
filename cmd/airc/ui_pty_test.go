package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestUIRealPTYConversationAndInputSafety(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("real PTY check needs Python 3")
	}
	address := chatServer(t, 64)
	question := posted(t, address, "planner", "#room", "What timeout should we use? Include the pinned deployment constraint.")
	obsolete := replyMessage(t, address, "reviewer", question.ID, "Use 10 seconds.")
	mustCLI(t, address, "correct", obsolete.ID, "--nick", "reviewer", "--message", "Use 30 seconds.")
	pin := posted(t, address, "planner", "#room", "Deploy only after the maintenance window.")
	mustCLI(t, address, "pin", pin.ID, "--nick", "planner")
	for i := range 10 {
		posted(t, address, "other", "#room", fmt.Sprintf("Unrelated conversation %d", i))
	}
	binary := filepath.Join(t.TempDir(), "airc")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", output, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, python, "testdata/ui_pty.py", binary, address, question.ID).CombinedOutput()
	if err != nil {
		t.Fatalf("PTY: %v\n%s", err, output)
	}
	t.Log(string(output))
}
