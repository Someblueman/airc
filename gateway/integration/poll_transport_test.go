//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestPollResultsMatchNativePoll maps poll.results to the native poll's
// question, options, votes and close state.
func TestPollResultsMatchNativePoll(t *testing.T) {
	f := newReactionHTTPFixture(t)
	want, pollID := seedAndClosePoll(t, f, 60)
	native := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == pollID })
	if native.Kind != "poll" || native.Target != "#gateway-fixture" || native.From != "fixture-author" || native.Message != want.Question || !reflect.DeepEqual(native.PollOptions, want.Options) || native.PollClosesAt.IsZero() || native.AccountID != want.AccountID {
		t.Fatalf("independent native poll observation mismatch: %+v", native)
	}
	checkPollTransports(t, f, want, pollID)
	if status, _ := pollHeaderGET(t, f, "/v1/poll?kind=room&target=%23gateway-fixture&poll_id="+strings.Repeat("a", 31)); status != http.StatusBadRequest {
		t.Fatalf("poll accepted malformed ID: status=%d", status)
	}
}

func TestPollResultsTargetAndMissingDenials(t *testing.T) {
	f := newReactionHTTPFixture(t)
	_, _ = seedAndClosePoll(t, f, 60)
	before := contextCheckSnapshot(t, f)
	_, foreignID := seedForeignPoll(t, f)
	wrong := "/v1/poll?kind=room&target=%23gateway-fixture&poll_id=" + url.QueryEscape(foreignID)
	if status, body := pollHeaderGET(t, f, wrong); status != http.StatusServiceUnavailable || !pollErrorCode(body, core.UpstreamUnavailable) {
		t.Fatalf("foreign poll was disclosed or untyped: status=%d body=%s", status, body)
	}
	missing := "/v1/poll?kind=room&target=%23gateway-fixture&poll_id=" + strings.Repeat("b", 32)
	if status, body := pollHeaderGET(t, f, missing); status != http.StatusServiceUnavailable || !pollErrorCode(body, core.UpstreamUnavailable) {
		t.Fatalf("missing poll returned no typed error: status=%d body=%s", status, body)
	}
	if after := contextCheckSnapshot(t, f); !reflect.DeepEqual(before, after) {
		t.Fatal("denials mutated native history")
	}
	if status, _ := headerRead(t, f, "/v1/requests/poll"); status != http.StatusNotFound {
		t.Fatalf("denial created receipt: %d", status)
	}
}
