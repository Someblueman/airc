//go:build darwin || linux

package service

import (
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// serviceAuthor is the guest the service fixtures' plain requests name.
const serviceAuthor = "service-guest"

// zeroMQServiceConfig serves one ZeroMQ listener with one session in all.
func zeroMQServiceConfig(t *testing.T) config.Config {
	t.Helper()
	c := upstreamServiceConfig(t)
	c.Limits.Sessions, c.Limits.SessionsPerAuthor = 1, 1
	c.Limits.Watches, c.Limits.WatchesPerAuthor = 0, 0
	c.Limits.Workers = 1
	c.Listeners = []config.Listener{{ID: "zeromq", Protocol: "zeromq", Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Send}}}
	return c
}

// serviceRequest is r as contract JSON naming the fixture's guest author.
func serviceRequest(t *testing.T, r core.Request) []byte {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	fields["author"] = map[string]string{"nick": serviceAuthor}
	if body, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	return body
}

// serviceJob reads the fixture guest author's job.
func serviceJob(t *testing.T, journal *store.Store, requestID string) (store.Job, error) {
	t.Helper()
	return journal.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: serviceAuthor}}, requestID)
}
