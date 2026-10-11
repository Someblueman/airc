package gateway_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// fixtureAuthor is the registered author most fixtures post as: aircd's
// account "fixture-author", provisioned with fixtureAccount as its token.
const fixtureAuthor = "fixture-author"

// frameRequest is a correlated request frame as a client writes it: the
// plain profile carries the contract request object (Request, JSON text)
// itself.
type frameRequest struct {
	Version       int
	CorrelationID string
	Profile       string
	Stream        bool
	Request       string
}

type frameRequestWire struct {
	Version       int             `json:"version"`
	CorrelationID string          `json:"correlation_id"`
	Profile       string          `json:"profile,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Request       json.RawMessage `json:"request,omitempty"`
}

func (f frameRequest) MarshalJSON() ([]byte, error) {
	wire := frameRequestWire{Version: f.Version, CorrelationID: f.CorrelationID, Profile: f.Profile, Stream: f.Stream}
	// Tests written against the two removed request profiles run as plain.
	if wire.Profile == "signed" || wire.Profile == "encrypted" {
		wire.Profile = "plain"
	}
	if f.Request != "" {
		wire.Request = json.RawMessage(f.Request)
	}
	return json.Marshal(wire)
}

func (f *frameRequest) UnmarshalJSON(data []byte) error {
	var wire frameRequestWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*f = frameRequest{Version: wire.Version, CorrelationID: wire.CorrelationID, Profile: wire.Profile, Stream: wire.Stream, Request: string(wire.Request)}
	return nil
}

// registeredSubmission is the journal key of a registered author.
func registeredSubmission(t *testing.T, nick, token string) store.Submission {
	t.Helper()
	credential, err := core.NewCredential(token)
	if err != nil {
		t.Fatal(err)
	}
	return store.Submission{Author: core.Author{Nick: nick, Registered: true}, Credential: credential}
}

// authorRequest is r as contract JSON naming an author: registered when token
// is set, a guest otherwise.
func authorRequest(t *testing.T, r core.Request, nick, token string) string {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	author := map[string]string{"nick": nick}
	if token != "" {
		author["token"] = token
	}
	fields["author"] = author
	if body, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// journalScope is the author scope a submission's jobs are keyed by.
func journalScope(t *testing.T, _ *store.Store, sub store.Submission) string {
	t.Helper()
	return sub.Author.Scope()
}

// authorSpecRequest is r as contract JSON naming the author spec "nick" (a
// guest) or "nick:token" (a registered account), the form fixtures keep in
// their token fields.
func authorSpecRequest(t *testing.T, r core.Request, spec string) string {
	t.Helper()
	nick, token, _ := strings.Cut(spec, ":")
	return authorRequest(t, r, nick, token)
}

// wrongAuthorSpec names the fixture author with a token AIRC refuses.
const wrongAuthorSpec = fixtureAuthor + ":ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

// detachedPayload is the operation-specific value of a DetachedResult
// ({"version","operation",<arm>}), as gRPC and JSON-RPC return it; other
// JSON is returned unchanged.
func detachedPayload(data []byte) []byte {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields["operation"] == nil || fields["version"] == nil || len(fields) != 3 {
		return data
	}
	for name, value := range fields {
		if name != "operation" && name != "version" {
			return value
		}
	}
	return data
}

// fixtureListenerLimits lifts the per-source and per-author request rates
// for fixtures that poll receipts many times within a minute.
func fixtureListenerLimits() *config.ListenerLimits {
	return &config.ListenerLimits{RequestsPerMinute: 10000, AuthorRequestsPerMinute: 10000}
}
