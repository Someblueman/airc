package server

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// expectNumeric reads the session's next line and checks its numeric and text.
func expectNumeric(t *testing.T, client *session, code, text string) {
	t.Helper()
	select {
	case line := <-client.out:
		command, _ := protocol.Parse(line)
		if command.Name != code || command.Trailing != text {
			t.Fatalf("got %q, want %s %q", line, code, text)
		}
	case <-time.After(time.Second):
		t.Fatalf("no %s answer", code)
	}
}

// A limit that a retry cannot clear answers 405 and a feature the daemon runs
// without answers 421, so a client stops rather than retrying; 437 is kept
// for what may pass later: a failed state write or a rate window.
func TestPermanentLimitsAndDisabledFeaturesAreNotRetryable(t *testing.T) {
	s := New(Config{})
	if err := s.EnableAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := range maxTotalChannels {
		s.channels[fmt.Sprintf("#room%d", i)] = map[string]*session{}
		s.watchers[fmt.Sprintf("#room%d", i)] = map[string]*session{}
		nick := fmt.Sprintf("bot%d", i)
		s.moderation[ruleKey("mute", nick, "*")] = protocol.ModerationRule{Kind: "mute", Nick: nick, Scope: "*", SetBy: "operator", SetAt: now}
		s.directory[nick] = protocol.AgentCard{Nick: nick, AgentProfile: protocol.AgentProfile{About: "kept"}}
	}

	client := directorySession("alice")
	client.channels, client.watching = map[string]struct{}{}, map[string]struct{}{}
	s.joinLocked(client, protocol.Command{Params: []string{"#new"}})
	expectNumeric(t, client, "405", "Server channel limit reached")
	s.observeLocked(client, protocol.Command{Params: []string{"#new"}})
	expectNumeric(t, client, "405", "Server observation limit reached")
	s.presenceLocked(client, protocol.Command{Params: []string{"thinking", "60"}, Trailing: "new card"})
	expectNumeric(t, client, "405", "Directory is full; clear unused profiles")
	s.profileLocked(client, protocol.Command{Trailing: `{"about":"new card"}`})
	expectNumeric(t, client, "405", "Directory is full; clear unused profiles")
	if _, exists := s.directory["alice"]; exists {
		t.Fatal("a refused card was stored")
	}

	operator := directorySession("operator")
	operator.admin = true
	request, _ := json.Marshal(protocol.AdminRequest{Action: "ban", Nick: "newbot"})
	s.adminLocked(operator, protocol.Command{Trailing: string(request)})
	expectNumeric(t, operator, "405", "Moderation rule limit reached")
	if _, banned := s.restrictionLocked("ban", "newbot", "*"); banned {
		t.Fatal("a refused rule was applied")
	}

	// Without an accounts file or wake notices the feature is absent, not busy.
	for _, request := range []protocol.AdminRequest{{Action: "account-list"}, {Action: "account-delete", Nick: "alice"}} {
		body, _ := json.Marshal(request)
		s.adminLocked(operator, protocol.Command{Trailing: string(body)})
		expectNumeric(t, operator, "421", "Accounts are not enabled on this daemon")
	}
	s.wakeLocked(client, protocol.Command{Params: []string{"STATUS"}})
	expectNumeric(t, client, "421", "Wake notices are not enabled on this daemon")

	// The per-address registration window stays a 437: it passes with time.
	registrant := &session{out: make(chan string, 4), done: make(chan struct{}), remoteKey: "203.0.113.9"}
	s.accountsAt = "unused"
	for range maxRegistrationsPerAddress {
		s.noteRegistrationLocked(registrant.remoteKey)
	}
	s.authLocked(registrant, protocol.Command{Name: "REGISTER", Params: []string{"newbie"}, Trailing: testAdminToken})
	expectNumeric(t, registrant, "437", "Account creation limit for this address reached; try again later")

	// The account limit is permanent, so it is a 405 like the others; the
	// per-connection window is checked first and stays a 437.
	full := &session{out: make(chan string, 4), done: make(chan struct{}), remoteKey: "203.0.113.10"}
	s.accounts = make(map[string]account, maxAccounts)
	for i := range maxAccounts {
		nick := fmt.Sprintf("holder%d", i)
		s.accounts[nickKey(nick)] = account{ID: newID(), Nick: nick}
	}
	s.authLocked(full, protocol.Command{Name: "REGISTER", Params: []string{"newbie"}, Trailing: testAdminToken})
	expectNumeric(t, full, "405", "Account limit reached")
	full.accountsMade = maxRegistrationsPerConnection
	s.authLocked(full, protocol.Command{Name: "REGISTER", Params: []string{"newbie"}, Trailing: testAdminToken})
	expectNumeric(t, full, "437", "Account creation limit for this connection reached")
}
