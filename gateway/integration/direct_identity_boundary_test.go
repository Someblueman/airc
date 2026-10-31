package gateway_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/pkg/irc"
)

// This is a native contract probe, not gateway DM-read acceptance. The gateway
// must not attribute old incoming messages to a new owner using a nickname alone.
func TestNativeDirectHistoryCannotIdentifyRecipientAccount(t *testing.T) {
	t.Parallel()
	socket := fixtureWithAdmin(t, true)
	const oldToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const newToken = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	const senderToken = "123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0"
	oldOwner := dial(t, socket, "fixture-recipient", oldToken, true, false)
	sender := dial(t, socket, "fixture-sender", senderToken, true, false)
	// A shared fixture-only room supplies a native disconnect acknowledgement.
	// Client.Close joins the local SDK, but not the daemon's nickname cleanup.
	for _, client := range []*irc.Client{oldOwner, sender} {
		if err := client.Join("#identity-boundary"); err != nil {
			t.Fatal(err)
		}
		next(t, client, func(e *irc.JoinEvent) bool { return e.Channel == "#identity-boundary" })
	}
	accountID := func(client *irc.Client, nick string) string {
		t.Helper()
		if err := client.Directory(nick); err != nil {
			t.Fatal(err)
		}
		card := next(t, client, func(e *irc.DirectoryEvent) bool { return e.Nick == nick })
		next(t, client, func(e *irc.EndOfDirectoryEvent) bool { return true })
		if card.AccountID == "" {
			t.Fatal("fixture account lacks identity", nick)
		}
		return card.AccountID
	}
	oldID := accountID(oldOwner, "fixture-recipient")
	senderID := accountID(sender, "fixture-sender")
	if err := sender.SendWithID("fixture-recipient", "belongs to the original recipient", "before-reassignment"); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, sender, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "before-reassignment" })
	if receipt.Receipt == nil || !receipt.Receipt.Accepted {
		t.Fatal("native DM was not accepted", receipt)
	}
	admin := dial(t, socket, "fixture-control", "", false, true)
	token, err := auth.ReadToken(filepath.Join(filepath.Dir(socket), "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.AuthenticateAdmin(token); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "381" })
	if err := admin.Moderate(irc.AdminRequest{Action: "account-delete", Nick: "fixture-recipient"}); err != nil {
		t.Fatal(err)
	}
	deleted := next(t, admin, func(e *irc.AdminEvent) bool { return e.Action == "account-delete" })
	if !deleted.Changed || deleted.AccountID != oldID {
		t.Fatal("fixture deletion failed", deleted)
	}
	next(t, admin, func(e *irc.EndOfAdminEvent) bool { return true })
	_ = oldOwner.Close()
	next(t, sender, func(e *irc.QuitEvent) bool { return e.Agent == "fixture-recipient" })
	newOwner := dial(t, socket, "fixture-recipient", newToken, true, true)
	newID := accountID(newOwner, "fixture-recipient")
	if newID == oldID {
		t.Fatal("fixture did not create a different recipient identity")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entries, err := irc.RequestCheck(ctx, newOwner, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "fixture-recipient", After: "*", Limit: 10}}, IncludeOwn: true, MaxMessages: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var retained *irc.MessageMetadata
	for _, entry := range entries {
		if entry.Kind == "message" && entry.Message != nil && entry.Message.ID == receipt.ID {
			retained = entry.Message
		}
	}
	if retained == nil || retained.Target != "fixture-recipient" || retained.From != "fixture-sender" || retained.AccountID != senderID || retained.Message != receipt.Message {
		t.Fatal("native recipient-boundary premise changed", retained)
	}
	t.Log("different recipient account retains the same nickname inbox record; message account_id identifies the sender, not the recipient")
}
