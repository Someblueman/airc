package gateway_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBackendParentIDsCannotBypassDestinationPermissions(t *testing.T) {
	t.Parallel()
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", token, false, true)
	peer := dial(t, socket, "fixture-peer", "", false, false)
	outsider := dial(t, socket, "fixture-outsider", "", false, false)
	if err := author.SendWithID("#forbidden-room", "private parent room", "foreign-room"); err != nil {
		t.Fatal(err)
	}
	roomParent := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "foreign-room" })
	if err := outsider.SendWithID("fixture-peer", "third-party DM", "foreign-dm"); err != nil {
		t.Fatal(err)
	}
	dmParent := next(t, outsider, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "foreign-dm" })
	next(t, peer, func(e *irc.MessageEvent) bool { return e.ID == dmParent.ID })
	cases := []struct {
		name, parent, kind, target string
		code                       core.ErrorCode
	}{
		{"room", roomParent.ID, "room", "#gateway-fixture", core.Forbidden},
		{"dm", dmParent.ID, "direct", "fixture-peer", core.Forbidden},
		{"missing", strings.Repeat("f", 32), "room", "#gateway-fixture", core.InvalidRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			proxy, done, _, replies := backendProxy(t, socket, "relay", nil)
			c, _ := acceptanceJournalFixture(t, proxy, token)
			identity := fixtureIdentity(t, c)
			b, err := backend.New(c)
			if err != nil {
				t.Fatal(err)
			}
			s, err := store.Open(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := core.Request{Version: 1, RequestID: "denied-parent", Operation: core.Reply, Target: &core.Target{Kind: test.kind, Name: test.target}, ParentID: test.parent, Message: "must never be submitted"}
			g, o := replyAttempt(t, s, b, identity, r)
			if o.Receipt != nil || o.Completion.MayHavePosted || o.Completion.Failure.Code != test.code || o.Completion.Failure.Transient {
				t.Fatal("parent permission bypass or wrong outcome", o)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("denied parent leaked a session")
			}
			if replies.Load() != 0 {
				t.Fatal("gateway wrote REPLY before authorizing the parent")
			}
			j, err := s.Complete(context.Background(), g, o.Completion, time.Now())
			if err != nil || j.Result.AcceptanceUncertain || j.Result.Accepted == nil || *j.Result.Accepted {
				t.Fatal("denied parent claimed unknown acceptance", j, err)
			}
		})
	}
}
