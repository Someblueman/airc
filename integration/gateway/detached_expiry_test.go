package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
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
