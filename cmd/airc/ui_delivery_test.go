package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
)

func TestUIDraftSurvivesServerRejection(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64, MaxMessageSize: 16})
	h := startUIBackend(t, address, "#room")
	h.until("online", func() bool { return h.model.connected })
	draft := "this is longer than sixteen bytes"
	cmds, _ := typeText(h.model, draft)
	for _, cmd := range cmds {
		h.cmds <- cmd
	}
	h.until("rejection", func() bool { return h.model.pending == nil && h.model.statusError })
	if string(h.model.input) != draft {
		t.Fatal("server rejection erased draft", string(h.model.input))
	}
	if got := mustCLI(t, address, "history", "#room", "--json"); strings.TrimSpace(got) != "" {
		t.Fatal("rejected message was posted", got)
	}
}

func TestUIAcceptedDMKeepsQueueAndDurabilityStatus(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	h := startUIBackend(t, address, "#room")
	h.until("online", func() bool { return h.model.connected })
	cmds, _ := typeText(h.model, "/msg absent hello")
	h.cmds <- cmds[0]
	h.until("receipt", func() bool { return h.model.pending == nil })
	if len(h.model.input) != 0 || !strings.Contains(h.model.status, "queued for absent") || !strings.Contains(h.model.status, "not persisted") {
		t.Fatal("receipt status lost", h.model.status)
	}
}

func TestUIRecoversLostReceiptWithoutPostingAgain(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 64})
	h := startUIBackend(t, dropFirstReceipt(t, address), "#room")
	h.until("online", func() bool { return h.model.connected })
	cmds, _ := typeText(h.model, "one accepted message")
	for _, cmd := range cmds {
		h.cmds <- cmd
	}
	h.until("unknown receipt", func() bool { return h.model.uncertainID != "" })
	if string(h.model.input) != "one accepted message" {
		t.Fatal("lost uncertain draft")
	}
	h.until("reconnected", func() bool { return h.model.connected && strings.Contains(h.model.status, "reconnected") })
	cmds, _ = h.model.update(keyIn{kind: keyEnter})
	if len(cmds) != 1 || cmds[0].kind != "retry-send" {
		t.Fatal("uncertain draft would post again", cmds)
	}
	h.cmds <- cmds[0]
	h.until("recovered receipt", func() bool { return h.model.pending == nil && h.model.uncertainID == "" })
	if len(h.model.input) != 0 {
		t.Fatal("receipt did not clear draft")
	}
	if got := historyIDs(t, mustCLI(t, address, "history", "#room", "--json")); len(got) != 1 {
		t.Fatal("recovery duplicated the post", got)
	}
}

func TestUIOfflineAndBusyRetryPreserveDraftAndRecovery(t *testing.T) {
	m := newTestModel("#room")
	m.input = []rune("draft")
	m.cursor = len(m.input)
	if cmds, _ := m.update(keyIn{kind: keyEnter}); len(cmds) != 0 || string(m.input) != "draft" || m.pending != nil {
		t.Fatal("offline draft was queued or lost")
	}
	m.uncertainID = "ui-original"
	m.update(keyIn{kind: keyRune, r: 'x'})
	if string(m.input) != "draft" {
		t.Fatal("unconfirmed draft was edited before receipt recovery")
	}
	if cmds, _ := m.update(keyIn{kind: keyEnter}); len(cmds) != 0 || m.uncertainID != "ui-original" {
		t.Fatal("offline recovery handle lost")
	}
	m.connected = true
	cmds, _ := m.update(keyIn{kind: keyEnter})
	m.update(deliveryIn{cmd: cmds[0], err: errors.New("busy")})
	if m.uncertainID != "ui-original" || string(m.input) != "draft" {
		t.Fatal("busy queue erased recovery handle")
	}
}

func TestUIFullCommandQueueKeepsSlashCommandDraft(t *testing.T) {
	m := newTestModel("#room")
	m.connected = true
	draft := "/topic keep this topic"
	m.input = []rune(draft)
	m.cursor = len(m.input)
	cursor := m.cursor
	cmds, _ := m.update(keyIn{kind: keyEnter})
	queue := make(chan uiCmd, 1)
	queue <- uiCmd{kind: "channels"}
	queueUICommands(m, queue, cmds, draft, cursor)
	if len(queue) != 1 || string(m.input) != draft || m.cursor != cursor {
		t.Fatal("full queue lost the slash command draft")
	}
}
