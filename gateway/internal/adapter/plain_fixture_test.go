package adapter

import (
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
)

// plainAuthor is the guest every plainFixture request names.
const plainAuthor = "plain-author"

// plainFixture is a route fixture with one listener's dispatcher, for
// carriers whose requests are plain contract JSON naming their author.
type plainFixture struct {
	*routeFixture
	d *dispatch.Dispatcher
}

func newPlainFixture(t *testing.T, l config.Listener) plainFixture {
	t.Helper()
	f := newRouteFixture(t)
	return plainFixture{f, f.listener(t, l)}
}

// plainRequest is one send every plain carrier test can post.
func plainRequest() core.Request {
	return core.Request{Version: 1, RequestID: "plain-request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "exact body\n世界"}
}

// body is r as contract JSON naming the fixture's guest author.
func (f plainFixture) body(t *testing.T, r core.Request) string {
	t.Helper()
	return string(authorRequestJSON(t, r, plainAuthor))
}

// lookup finds the guest author's job.
func (f plainFixture) lookup(t *testing.T, requestID string) (store.Job, error) {
	t.Helper()
	return f.s.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: plainAuthor}}, requestID)
}

// authorRequestJSON is r as contract-v1 JSON naming nick as a guest author.
func authorRequestJSON(t *testing.T, r core.Request, nick string) []byte {
	t.Helper()
	return requestJSON(t, r, map[string]string{"nick": nick})
}

// requestJSON is r as contract-v1 JSON naming author, or no author when nil.
func requestJSON(t *testing.T, r core.Request, author map[string]string) []byte {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if author != nil {
		fields["author"] = author
	}
	if body, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	return body
}
