package dispatch

import (
	"crypto/sha256"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const gateToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func gateFixture(t *testing.T, l config.Listener) (*Dispatcher, *store.Store) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "gate", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "airc.sock")}
	if l.PasswordFile != "" {
		path := filepath.Join(root, "password")
		if err := os.WriteFile(path, []byte(l.PasswordFile+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		l.PasswordFile = path
	}
	l.ID, l.Enabled = "web", true
	c.Listeners = []config.Listener{l}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	d, err := ForListener(s, v, b, nil, c.Listeners[0])
	if err != nil {
		t.Fatal(err)
	}
	return d, s
}

func send(id string) core.Request {
	return core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#ops"}, Message: "hello"}
}

func TestListenerPolicyDefaults(t *testing.T) {
	d, _ := gateFixture(t, config.Listener{Protocol: "http"})
	if !d.AuthorAccess() || !d.gate.guests || d.gate.password != nil || d.gate.targets != nil {
		t.Fatal("zero config must admit guests without a password or target allowlist", d.gate)
	}
	for _, op := range []core.Operation{core.Send, core.ReceiptGet, core.AccountRegister, core.AccountIdentify, core.CapabilitiesGet, core.ContextRead} {
		if !d.enabled(actor{author: &store.Submission{}, gate: d.gate}, op) {
			t.Errorf("default operations lack %s", op)
		}
	}
	// History needs a cursor key; without one it is not offered.
	if d.enabled(actor{author: &store.Submission{}, gate: d.gate}, core.HistoryRead) {
		t.Error("history offered without a cursor key")
	}
	now := time.Unix(1000, 0)
	guest := core.Author{Nick: "Visitor"}
	result, err := d.Admit(t.Context(), "web", "203.0.113.5", guest, core.Credential{}, send("one"), now)
	if err != nil || result.Result == nil || result.Result.State != core.Queued {
		t.Fatal("guest post", result, err)
	}
	// receipt.get: a guest's job by nickname; the same request ID is one job.
	lookup := core.Request{Version: 1, RequestID: "status", Operation: core.ReceiptGet, LookupID: "one"}
	if got, err := d.Admit(t.Context(), "other", "198.51.100.1", core.Author{Nick: "visitor"}, core.Credential{}, lookup, now); err != nil || got.Result.RequestID != "one" {
		t.Fatal("guest receipt", got, err)
	}
	if _, err := d.Admit(t.Context(), "web", "203.0.113.5", core.Author{Nick: "stranger"}, core.Credential{}, lookup, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("another nickname saw the guest's receipt", err)
	}
	credential, _ := core.NewCredential(gateToken)
	writer := core.Author{Nick: "writer", Registered: true}
	if _, err := d.Admit(t.Context(), "web", "203.0.113.5", writer, credential, send("two"), now); err != nil {
		t.Fatal(err)
	}
	lookup.LookupID = "two"
	other, _ := core.NewCredential(gateToken[1:] + "0")
	if _, err := d.Admit(t.Context(), "web", "203.0.113.5", writer, other, lookup, now); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("registered receipt without the matching token", err)
	}
	if got, err := d.Admit(t.Context(), "web", "203.0.113.5", writer, credential, lookup, now); err != nil || got.Result.RequestID != "two" {
		t.Fatal("registered receipt", err)
	}
	for name, c := range map[string]struct {
		author core.Author
		cred   core.Credential
		r      core.Request
		want   error
	}{
		"no author":                {core.Author{}, core.Credential{}, send("x"), store.ErrInvalid},
		"registered without token": {writer, core.Credential{}, send("x"), store.ErrInvalid},
		"guest with token":         {guest, credential, send("x"), store.ErrInvalid},
		"guest registers":          {guest, core.Credential{}, core.Request{Version: 1, RequestID: "r", Operation: core.AccountRegister}, store.ErrInvalid},
	} {
		if _, err := d.Admit(t.Context(), "web", "203.0.113.5", c.author, c.cred, c.r, now); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if caps, err := d.Admit(t.Context(), "web", "203.0.113.5", core.Author{}, core.Credential{}, core.Request{Version: 1, RequestID: "c", Operation: core.CapabilitiesGet}, now); err != nil || caps.Capabilities.Listener == nil || !caps.Capabilities.Listener.AnyTarget || !caps.Capabilities.Listener.Guests {
		t.Fatal("capabilities without an author", caps, err)
	}
}

func TestListenerPolicyRefusals(t *testing.T) {
	no := false
	d, _ := gateFixture(t, config.Listener{Protocol: "websocket", Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#ops"}}, Guests: &no, PasswordFile: "sesame", Limits: &config.ListenerLimits{RequestsPerMinute: 3, AuthorRequestsPerMinute: 2}})
	now := time.Unix(1000, 0)
	credential, _ := core.NewCredential(gateToken)
	in := Ingress{Source: "203.0.113.5"}
	body := func(r core.Request, extra string) core.Submission {
		author := core.Author{Nick: "w" + r.RequestID, Registered: true}
		sub := core.Submission{Request: r, Author: &author, Credential: credential}
		if extra != "" {
			sub.Password, _ = core.NewPassword(extra)
		}
		return sub
	}
	if _, err := d.SubmitDecoded(t.Context(), in, body(send("a"), ""), now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("missing access password", err)
	}
	if _, err := d.SubmitDecoded(t.Context(), in, body(send("a"), "wrong"), now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("wrong access password", err)
	}
	if _, err := d.SubmitDecoded(t.Context(), in, body(send("a"), "sesame"), now); err != nil {
		t.Fatal("correct access password", err)
	}
	// A connection presents the password once, like IRC's PASS.
	session := NewSession()
	conn := Ingress{Source: "203.0.113.6", Session: session}
	if _, err := d.SubmitDecoded(t.Context(), conn, body(send("b"), "sesame"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SubmitDecoded(t.Context(), conn, body(send("c"), ""), now); err != nil {
		t.Fatal("admitted connection asked for the password again", err)
	}
	session.Close()
	if _, err := d.SubmitDecoded(t.Context(), conn, body(send("d"), ""), now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("closed session kept its admission", err)
	}
	pass := Ingress{Source: "203.0.113.7", Password: body(send("x"), "sesame").Password}
	guest := core.Submission{Request: send("g"), Author: &core.Author{Nick: "visitor"}}
	if _, err := d.SubmitDecoded(t.Context(), pass, guest, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("guest admitted with guests disabled", err)
	}
	other := send("t")
	other.Target = &core.Target{Kind: "room", Name: "#elsewhere"}
	if _, err := d.SubmitDecoded(t.Context(), pass, body(other, ""), now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("target outside the allowlist", err)
	}
	read := core.Request{Version: 1, RequestID: "r", Operation: core.PinsRead, Target: &core.Target{Kind: "room", Name: "#ops"}}
	if _, err := d.SubmitDecoded(t.Context(), pass, body(read, ""), now); !errors.Is(err, ErrUnsupported) {
		t.Fatal("operation outside the listener's list", err)
	}
	// Flood limits: per author (2/min), then per source (3/min).
	limited := Ingress{Source: "198.51.100.9", Password: pass.Password}
	reader := core.Author{Nick: "reader", Registered: true}
	for i, want := range []error{nil, nil, store.ErrCapacity} {
		sub := core.Submission{Request: send("f" + string(rune('a'+i))), Author: &reader, Credential: credential}
		if _, err := d.SubmitDecoded(t.Context(), limited, sub, now); want == nil && err != nil || want != nil && !errors.Is(err, want) {
			t.Fatalf("author request %d: %v", i, err)
		}
	}
	if _, err := d.SubmitDecoded(t.Context(), limited, body(send("s1"), ""), now); !errors.Is(err, store.ErrCapacity) {
		t.Fatal("source flood not limited", err)
	}
	if _, err := d.SubmitDecoded(t.Context(), limited, body(send("s1"), ""), now.Add(time.Minute)); err != nil {
		t.Fatal("limit did not refill", err)
	}
}

func TestSources(t *testing.T) {
	d, _ := gateFixture(t, config.Listener{Protocol: "unix", Address: "/tmp/unused.sock"})
	if got := d.Source(&net.TCPAddr{IP: net.ParseIP("::ffff:203.0.113.5"), Port: 9}); got != "203.0.113.5" {
		t.Fatal(got)
	}
	if got := d.Source(&net.UnixAddr{Name: "@", Net: "unix"}); got != "listener:web" {
		t.Fatal(got)
	}
	if got := d.RemoteSource("[2001:db8::1]:443"); got != "2001:db8::1" {
		t.Fatal(got)
	}
	digest := sha256.Sum256([]byte("x"))
	if p, _ := core.NewPassword("x"); !p.Matches(digest) {
		t.Fatal("password digest")
	}
}
