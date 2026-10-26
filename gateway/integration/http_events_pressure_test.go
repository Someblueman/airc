package gateway_test

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPEventsAbandonedHTTP2BodyReleasesLease(t *testing.T) {
	f := gatewayHTTPFixture(t, true, "watch")
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	author := dial(t, f.socket, "fixture-author", account, false, true)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/v1/events?kind=room&target=%23gateway-fixture&limit=1", nil)
	setAuthorHeaders(req, f.token)
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewScanner(response.Body)
	ssePage(t, reader)
	// Stop consuming the response. Exceed the HTTP/2 receive window with real
	// upstream records; synchronous deadline-bound writes must release the lease.
	body := strings.Repeat("x", 4000)
	for i := 0; i < 128; i++ {
		id := fmt.Sprint("abandoned-", i)
		if err := author.SendWithID("#gateway-fixture", body, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	select {
	case <-f.watchJoined:
	case <-time.After(7 * time.Second):
		t.Fatal("slow reader retained upstream lease past write bound")
	}
	status, _, _ := httpCall(t, f, http.MethodGet, "/v1/capabilities", f.token, nil)
	if status != 200 {
		t.Fatal("abandoned stream blocked independent caller", status)
	}
}
