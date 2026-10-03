package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestUICrashAfterAcceptanceChild(t *testing.T) {
	address := os.Getenv("AIRC_TEST_UI_CRASH_ADDR")
	if address == "" {
		t.Skip("subprocess only")
	}
	opt := options{nick: "crash-human", addr: address}
	m := newUIModel(opt.nick, []string{"#room"}, time.Now)
	m.connected = true
	s, err := openUIState(opt, m)
	if err != nil {
		t.Fatal(err)
	}
	cmds, _ := typeText(m, "survives abrupt process exit")
	if err := prepareUIDelivery(opt, m, cmds); err != nil {
		t.Fatal(err)
	}
	if err := s.save(m); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialOneShot(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	cmd := cmds[0]
	if err := c.SendWithID(cmd.target, cmd.text, cmd.requestID); err != nil {
		t.Fatal(err)
	}
	if _, err := awaitSend(ctx, c, opt.nick, cmd.target, cmd.text, "", "", cmd.requestID, nil); err != nil {
		t.Fatal(err)
	}
	// Terminate without saving the receipt, updating the UI, or running defers.
	os.Exit(73)
}

func TestUICrashRecoveryUsesSameAcceptedID(t *testing.T) {
	address := chatServer(t, 64)
	child := exec.Command(os.Args[0], "-test.run=^TestUICrashAfterAcceptanceChild$")
	child.Env = append(os.Environ(), "AIRC_TEST_UI_CRASH_ADDR="+address)
	output, err := child.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 73 {
		t.Fatalf("child did not reach crash boundary: %v %s", err, output)
	}
	opt := options{nick: "crash-human", addr: address}
	m := newUIModel(opt.nick, []string{"#room"}, time.Now)
	s, err := openUIState(opt, m)
	if err != nil {
		t.Fatal(err)
	}
	defer s.lock.Close()
	if m.uncertainID == "" || string(m.input) != "survives abrupt process exit" {
		t.Fatal("crash lost durable state")
	}
	before := historyIDs(t, mustCLI(t, address, "history", "#room", "--json"))
	if len(before) != 1 {
		t.Fatal("missing accepted post", before)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialOneShot(ctx, opt)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b := uiBackend{opt: opt, nick: opt.nick}
	status, _, err := b.sendConfirmed(ctx, c, uiCmd{kind: "retry-send", target: m.uncertainID}, nil)
	if err != nil {
		t.Fatal(status, err)
	}
	after := historyIDs(t, mustCLI(t, address, "history", "#room", "--json"))
	if len(after) != 1 || after[0] != before[0] {
		t.Fatal("crash recovery duplicated post", after)
	}
}
