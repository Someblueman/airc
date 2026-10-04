package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func dial(t *testing.T, socket, nick, token string, create, ephemeral bool) *irc.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: socket, Nick: nick, IdentityToken: token, CreateAccount: create, Ephemeral: ephemeral, ReadTimeout: 10 * time.Second, WriteTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func next[T irc.Event](t *testing.T, c *irc.Client, match func(T) bool) T {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-c.Events():
			if !ok {
				t.Fatal("fixture client disconnected")
			}
			if e, ok := event.(T); ok && match(e) {
				return e
			}
		case <-timer.C:
			t.Fatal("expected upstream event timed out")
		}
	}
}

func TestPinnedUpstream(t *testing.T) {
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" && e.Channel == "#gateway-fixture" })
	// Provision only a disposable fixture account, then authenticate without
	// account creation, exactly as the gateway backend must do.
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", token, false, true)
	if !author.Ephemeral() {
		t.Fatal("ephemeral mode not negotiated")
	}
	for _, feature := range []string{"RECEIPTS", "IDEMPOTENCY", "SAFE_RETRY", "REPLIES"} {
		if author.Features()[feature] != "1" {
			t.Fatalf("required %s=1; negotiated %v", feature, author.Features())
		}
	}
	const requestID = "fixture-stable-id"
	const body = "gateway fixture\nindependent observation"
	if err := author.SendWithID("#gateway-fixture", body, requestID); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == requestID })
	if receipt.Receipt == nil || !receipt.Receipt.Accepted || !receipt.Receipt.Persisted || receipt.From != "fixture-author" || receipt.Target != "#gateway-fixture" || receipt.Message != body || receipt.ID == "" {
		t.Fatalf("invalid accepted receipt: %+v", receipt)
	}
	observed := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
	if observed.From != receipt.From || observed.Target != receipt.Target || observed.Message != body {
		t.Fatalf("observer disagrees: %+v", observed)
	}
	if err := author.RetryRequest(requestID); err != nil {
		t.Fatal(err)
	}
	recovered := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == requestID })
	if recovered.ID != receipt.ID {
		t.Fatalf("lookup changed message ID: %s != %s", recovered.ID, receipt.ID)
	}
	if err := author.SendWithID("#gateway-fixture", body, requestID); err != nil {
		t.Fatal(err)
	}
	repeated := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == requestID })
	if repeated.ID != receipt.ID {
		t.Fatal("retained stable ID did not deduplicate")
	}
	if err := author.ReplyWithID(receipt.ID, "fixture reply", "fixture-reply-id"); err != nil {
		t.Fatal(err)
	}
	reply := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "fixture-reply-id" })
	if reply.ReplyTo != receipt.ID || reply.ThreadID != receipt.ID || reply.Target != receipt.Target || reply.From != receipt.From || reply.Message != "fixture reply" || reply.Receipt == nil || !reply.Receipt.Accepted || !reply.Receipt.Persisted {
		t.Fatalf("invalid reply receipt: %+v", reply)
	}
	replyObserved := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == reply.ID })
	if replyObserved.ReplyTo != receipt.ID || replyObserved.ThreadID != receipt.ID || replyObserved.Message != reply.Message || replyObserved.From != reply.From || replyObserved.Target != reply.Target {
		t.Fatalf("observer reply disagrees: %+v", replyObserved)
	}
	t.Logf("registered author, independent observer, accepted+persisted receipt, lookup and retained-ID retry verified: message=%s", receipt.ID)
}
