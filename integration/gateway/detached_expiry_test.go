package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestDetachedReadCannotOutliveSignedValidity(t *testing.T) {
	f := httpGatewayFixture(t, true, nil, false, true, "withhold-history")
	r := core.Request{Version: 1, RequestID: "expiry-read", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1}
	// Auth accepts within its documented 60-second clock allowance. The lease
	// must still end at expiry+allowance, not gain another 15 seconds.
	now := time.Now().Unix()
	expiry := now - 58
	token := f.credentials.validityToken(t, r, expiry-30, expiry, false)
	began := time.Now()
	status, _, result := detachedCall(t, f, token, false)
	if status != 503 || result.Page != nil || time.Since(began) > 3*time.Second {
		t.Fatal("signed read exceeded validity or returned partial data", status, result, time.Since(began))
	}
	select {
	case <-f.watchJoined:
	case <-time.After(time.Second):
		t.Fatal("expired read retained upstream work")
	}
}

func TestDetachedReceiptCannotOutliveSignedValidity(t *testing.T) {
	c, identity := acceptanceJournalFixture(t, "/unused-fixture.sock", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	credentials := provisionDetached(t, &c)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := dispatch.New(s, v, nil, nil, []core.Operation{core.Send, core.ReceiptGet})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r := core.Request{Version: 1, RequestID: "receipt-expiry", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "local receipt fixture"}
	if _, _, err := s.Admit(context.Background(), identity, r, 0, now); err != nil {
		t.Fatal(err)
	}
	r = core.Request{Version: 1, RequestID: "lookup", Operation: core.ReceiptGet, LookupID: r.RequestID}
	for _, encrypted := range []bool{false, true} {
		fresh := credentials.token(t, r, encrypted)
		if result, err := d.Detached(context.Background(), fresh, encrypted, now); err != nil || result.Result == nil {
			t.Fatal("valid receipt lookup failed", encrypted, result, err)
		}
		// Verification's injected clock accepts the envelope within skew, while
		// the operation's real deadline has passed. This models crossing the
		// validity boundary between authentication and the journal read.
		expiry := now.Unix() - auth.ClockSkewSeconds - 1
		token := credentials.validityToken(t, r, expiry-30, expiry, encrypted)
		verificationTime := time.Unix(expiry+auth.ClockSkewSeconds-1, 0)
		result, err := d.Detached(context.Background(), token, encrypted, verificationTime)
		if !errors.Is(err, context.DeadlineExceeded) || result.Result != nil {
			t.Fatal("receipt escaped signed deadline", encrypted, result, err)
		}
	}
	if job, err := s.Lookup(context.Background(), identity, r.LookupID); err != nil || job.Result.SendAttempts != 0 {
		t.Fatal("receipt lookup changed delivery state", job, err)
	}
}
