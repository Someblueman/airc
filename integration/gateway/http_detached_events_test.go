package gateway_test

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func detachedEvents(t *testing.T, f httpFixture, ctx context.Context, r core.Request, encrypted bool, expiry int64) (*http.Response, *bufio.Scanner) {
	t.Helper()
	route := "signed"
	if encrypted {
		route = "encrypted"
	}
	token := f.credentials.token(t, r, encrypted)
	if expiry != 0 {
		token = f.credentials.validityToken(t, r, expiry-30, expiry, encrypted)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.server.URL+"/v1/envelopes/"+route+"/events", strings.NewReader(token))
	req.Header.Set("Content-Type", "application/jose")
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	reader := bufio.NewScanner(response.Body)
	reader.Buffer(make([]byte, 4096), backend.MaxHistoryBytes)
	return response, reader
}

func TestHTTPDetachedEventsLiveResumeExpiryAndRevocation(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, encrypted := range []bool{false, true} {
			t.Run(fmt.Sprint(h2, "/", encrypted), func(t *testing.T) {
				f := httpGatewayFixture(t, h2, nil, false, true)
				const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
				author := dial(t, f.socket, "fixture-author", account, false, true)
				r := core.Request{Version: 1, RequestID: "stream", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1, Start: "retained"}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				response, reader := detachedEvents(t, f, ctx, r, encrypted, 0)
				if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || (response.ProtoMajor == 2) != h2 {
					t.Fatal("detached stream negotiation failed", response.Status, response.Proto)
				}
				page, _ := ssePage(t, reader)
				if len(page.Messages) != 0 {
					t.Fatal("empty retained snapshot was not empty", page)
				}
				if err := author.SendWithID(r.Target.Name, "signed stream\nwhole body 🙂", "detached-live"); err != nil {
					t.Fatal(err)
				}
				receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "detached-live" })
				page, cursor := ssePage(t, reader)
				if len(page.Messages) != 1 || page.Messages[0].ID != receipt.ID || page.Messages[0].Message != receipt.Message || cursor != page.Cursor || !strings.HasPrefix(cursor, "gc1.") {
					t.Fatal("signed stream lost live message or scoped cursor", page, cursor)
				}
				cancel()
				_ = response.Body.Close()
				if err := author.SendWithID(r.Target.Name, "offline continuation", "detached-offline"); err != nil {
					t.Fatal(err)
				}
				receipt = next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "detached-offline" })
				r.Start, r.Cursor = "", cursor
				// A new envelope resumes its signed cursor and ends at expiry+skew.
				ctx, cancel = context.WithTimeout(t.Context(), 8*time.Second)
				defer cancel()
				began := time.Now()
				response, reader = detachedEvents(t, f, ctx, r, encrypted, began.Unix()-57)
				page, _ = ssePage(t, reader)
				if response.StatusCode != 200 || len(page.Messages) != 1 || page.Messages[0].ID != receipt.ID {
					t.Fatal("signed cursor resume lost retained continuation", page, response.Status)
				}
				expired := false
				for reader.Scan() {
					if reader.Text() == `data: {"reason":"lease_expired"}` {
						expired = true
					}
				}
				if !expired || reader.Err() != nil || time.Since(began) > 4*time.Second {
					t.Fatal("signed stream escaped validity", expired, reader.Err(), time.Since(began))
				}
				_ = response.Body.Close()
				if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
					t.Fatal(err)
				}
				response, _ = detachedEvents(t, f, t.Context(), r, encrypted, 0)
				if response.StatusCode != 403 {
					t.Fatal("stream renewal escaped revocation", response.Status)
				}
			})
		}
	}
}
