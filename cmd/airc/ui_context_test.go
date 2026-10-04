package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestUISelectedReplyDoesNotDriftWithLiveTraffic(t *testing.T) {
	m := newTestModel("#room")
	question := uiMessage(strings.Repeat("a", 32), "planner", "#room", "Question\nwith controls\x1b[2J", 0)
	m.update(msgIn{event: question})
	m.update(keyIn{kind: keySelectPrev})
	if strings.ContainsAny(m.status, "\n\x1b") {
		t.Fatal("selection preview contains terminal controls")
	}
	m.update(keyIn{kind: keyCtrl, r: 'r'})
	newer := uiMessage(strings.Repeat("b", 32), "peer", "#room", "Unrelated new post", time.Second)
	m.update(msgIn{event: newer})
	cmds, _ := typeText(m, "answer")
	if len(cmds) != 1 || cmds[0].kind != "reply" || cmds[0].target != question.ID {
		t.Fatal("reply target drifted", cmds)
	}
}

func TestUIContextShowsPinsCorrectionsAndOmissions(t *testing.T) {
	m := newTestModel("#room")
	root, old, correction, pin := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32), strings.Repeat("d", 32)
	snapshot := irc.ConversationContext{TriggerID: old, RootID: root, OmittedMessages: 7, OmittedPins: 2, Missing: []string{root}}
	snapshot.Messages = []irc.MessageMetadata{{ID: old, Message: "old value", Target: "#room", From: "peer", ThreadID: root}, {ID: correction, Message: "correct value", Target: "#room", From: "peer", ThreadID: root}}
	snapshot.Messages[0].SupersededBy = correction
	snapshot.Messages[1].Supersedes = old
	snapshot.Messages[1].Kind = "correct"
	snapshot.Pins = []irc.MessageMetadata{{ID: pin, Message: "pinned constraint", Target: "#room", From: "planner"}}
	m.update(contextIn{snapshot})
	b := m.cur()
	if b.selectedID != old || len(b.pins) != 1 || b.pins[0].Message != "pinned constraint" {
		t.Fatal("context lost selection or pins")
	}
	if !strings.Contains(b.topic, "omitted 7 messages, 2 pins") || !strings.Contains(b.topic, root) {
		t.Fatal("missing omission/retention status", b.topic)
	}
	lines := strings.Join(m.logLines(b, 120), "\n")
	for _, s := range []string{"Pinned", pin, "pinned constraint", "old value", "correct value"} {
		if !strings.Contains(lines, s) {
			t.Fatalf("context missing %q", s)
		}
	}
	m.update(keyIn{kind: keyCtrl, r: 'r'})
	if m.replyTo != old {
		t.Fatal("context replies retargeted the root instead of the trigger")
	}
}
