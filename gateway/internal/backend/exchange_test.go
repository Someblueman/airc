package backend

import (
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPinnedNumericClassification(t *testing.T) {
	a := store.Attempt{Kind: store.SendAttempt}
	for _, ignored := range []string{"422", "001", "005", "903", "NOTICE"} {
		if _, handled := numericFailure(a, &irc.RawEvent{Command: ignored}); handled {
			t.Fatal("registration/non-error event rejected", ignored)
		}
	}
	o, handled := numericFailure(a, &irc.RawEvent{Command: "486", Trailing: "Slow mode: retry after 12 seconds"})
	if !handled || o.Completion.Failure.Code != core.CapacityExceeded || o.Completion.MayHavePosted || o.Completion.RetryAfterSeconds != 12 {
		t.Fatal("rate limit lost delay", o)
	}
	o, _ = numericFailure(a, &irc.RawEvent{Command: "486", Trailing: "unknown grammar"})
	if !o.Completion.MayHavePosted || o.Completion.RetryAfterSeconds != 0 || o.Completion.Failure.UpstreamCode != 486 {
		t.Fatal("unrecognized error falsely proved no post", o)
	}
	o, _ = numericFailure(a, &irc.RawEvent{Command: "487"})
	if o.Completion.Failure.Code != core.IDConflict || o.Completion.MayHavePosted {
		t.Fatal("conflict classification", o)
	}
	o, _ = numericFailure(a, &irc.RawEvent{Command: "485"})
	if o.Completion.Failure.Code != core.Forbidden || o.Completion.Failure.Transient || o.Completion.MayHavePosted || o.Completion.Failure.UpstreamCode != 485 {
		t.Fatal("native mute did not block the denied write", o)
	}
	a.Kind = store.LookupAttempt
	o, _ = numericFailure(a, &irc.RawEvent{Command: "488"})
	if o.Completion.Failure.Code != core.DeliveryUncertain || !o.Completion.Failure.Transient || o.Completion.MayHavePosted {
		t.Fatal("unknown lookup became rejection or write", o)
	}
	o = registrationFailure(a, &irc.RejectedError{Code: "437"})
	if o.Completion.Failure.Code != core.CapacityExceeded || !o.Completion.Failure.Transient {
		t.Fatal("temporary registration capacity became permanent", o)
	}
	a.Kind = store.SendAttempt
	o, _ = numericFailure(a, &irc.RawEvent{Command: "437", Trailing: "Slow-mode state full; retry later"})
	if o.Completion.Failure.Code != core.CapacityExceeded || !o.Completion.Failure.Transient || o.Completion.MayHavePosted {
		t.Fatal("transient capacity became permanent", o)
	}
	o, _ = numericFailure(a, &irc.RawEvent{Command: "405", Trailing: "Server channel limit reached"})
	if o.Completion.Failure.Code != core.CapacityExceeded || o.Completion.Failure.Transient || o.Completion.MayHavePosted || o.Completion.Failure.UpstreamCode != 405 {
		t.Fatal("a permanent limit was left retryable", o)
	}
}

// TestMutationTable checks that every durable operation has an exchange and
// that only operations with a native request-ID lookup accept lookup grants.
func TestMutationTable(t *testing.T) {
	for op, m := range mutations {
		if !op.IsMutation() || m.exchange == nil || m.lookup != (op == core.Send || op == core.Reply) || m.lookup && !op.HasLookup() {
			t.Fatalf("%s: %+v", op, m)
		}
	}
	if len(mutations) != 18 {
		t.Fatalf("%d mutations have an exchange", len(mutations))
	}
}
