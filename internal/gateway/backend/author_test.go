package backend

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

const sessionToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func authorBackend() (*Backend, config.Upstream) {
	pinned := config.Upstream{Network: "unix", Address: "/run/airc.sock"}
	u := upstream{pinned: pinned, dial: irc.Config{Network: "unix", Addr: pinned.Address, AccessToken: "access", Ephemeral: true}}
	return &Backend{upstream: &u}, pinned
}

// Each job's session is built from its author over one shared dial template.
func TestSessionConfigPerJobAuthor(t *testing.T) {
	b, pinned := authorBackend()
	token, err := core.NewCredential(sessionToken)
	if err != nil {
		t.Fatal(err)
	}
	guest, ok := b.sessionConfig(pinned, core.Author{Nick: "Visitor"}, core.Credential{}, "")
	if !ok || guest.Nick != "Visitor" || guest.IdentityToken != "" || !guest.Ephemeral || guest.CreateAccount || guest.Addr != pinned.Address || guest.AccessToken != "access" {
		t.Fatalf("guest session %+v", guest)
	}
	author, ok := b.sessionConfig(pinned, core.Author{Nick: "Writer", Registered: true}, token, "")
	if !ok || author.Nick != "Writer" || author.IdentityToken != sessionToken || !author.Ephemeral || author.AccessToken != "access" {
		t.Fatalf("registered session %+v", author)
	}
	other := pinned
	other.Address = "/elsewhere.sock"
	for name, ok := range map[string]bool{
		"guest with token":         allowed(b.sessionConfig(pinned, core.Author{Nick: "Visitor"}, token, "")),
		"registered without token": allowed(b.sessionConfig(pinned, core.Author{Nick: "Writer", Registered: true}, core.Credential{}, "")),
		"invalid nick":             allowed(b.sessionConfig(pinned, core.Author{Nick: "1bad"}, core.Credential{}, "")),
		"rebound upstream":         allowed(b.sessionConfig(other, core.Author{Nick: "Visitor"}, core.Credential{}, "")),
	} {
		if ok {
			t.Fatal("session allowed:", name)
		}
	}
	// With aircd's gateway credential, every author session asserts its
	// client's source.
	b.upstream.gateway = strings.Repeat("9", 64)
	sourced, ok := b.sessionConfig(pinned, core.Author{Nick: "Visitor"}, core.Credential{}, "203.0.113.7")
	if !ok || sourced.GatewayToken != b.upstream.gateway || sourced.Source != "203.0.113.7" {
		t.Fatalf("gateway session %+v", sourced)
	}
	if _, ok := b.sessionConfig(pinned, core.Author{Nick: "Visitor"}, core.Credential{}, "203.0.113.7:80"); ok {
		t.Fatal("session with an invalid source")
	}
	b.upstream.gateway = ""
	// Without an upstream, authors are refused.
	b.upstream = nil
	if _, ok := b.sessionConfig(pinned, core.Author{Nick: "Visitor"}, core.Credential{}, ""); ok {
		t.Fatal("author session without an upstream")
	}
}

func allowed(_ irc.Config, ok bool) bool { return ok }

func TestRegistrationRefusalByAuthor(t *testing.T) {
	guest := store.Attempt{Kind: store.SendAttempt, Job: store.Job{Author: core.Author{Nick: "visitor"}}}
	registered := store.Attempt{Kind: store.SendAttempt, Job: store.Job{Author: core.Author{Nick: "writer", Registered: true}}}
	for _, c := range []struct {
		attempt store.Attempt
		numeric string
		code    core.ErrorCode
	}{
		{guest, "498", core.NickRegistered},
		{registered, "498", core.AuthenticationFailed},
		{registered, "904", core.AuthenticationFailed},
		{guest, "464", core.AuthenticationFailed},
	} {
		o := registrationFailure(c.attempt, &irc.RejectedError{Code: c.numeric})
		f := o.Completion.Failure
		if f.Code != c.code || f.Transient || o.Completion.MayHavePosted || f.UpstreamCode == 0 {
			t.Fatalf("%s for registered=%v: %+v", c.numeric, c.attempt.Job.Author.Registered, o.Completion)
		}
	} // A throttled refusal never judged the credential: retry later, for
	// guests and registered authors alike.
	for _, a := range []store.Attempt{guest, registered} {
		for _, numeric := range []string{"498", "904"} {
			o := registrationFailure(a, &irc.RejectedError{Code: numeric, Throttled: true})
			if f := o.Completion.Failure; f.Code != core.CapacityExceeded || !f.Transient || o.Completion.MayHavePosted {
				t.Fatalf("throttled %s for registered=%v: %+v", numeric, a.Job.Author.Registered, o.Completion)
			}
		}
	}
}
