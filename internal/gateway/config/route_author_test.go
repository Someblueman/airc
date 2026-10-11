package config

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

func TestRouteAuthorPolicy(t *testing.T) {
	yes, no := true, false
	bound := &BoundAuthor{Nick: "alerts", AccountTokenFile: "/tmp/alerts.token"}
	for _, c := range []struct {
		bound  *BoundAuthor
		guests *bool
		want   bool
	}{{nil, nil, true}, {bound, nil, false}, {bound, &yes, true}, {nil, &no, false}} {
		if RouteGuests(c.bound, c.guests) != c.want {
			t.Fatal(c)
		}
	}
	if err := validRouteAuthor(nil, &no); !errors.Is(err, ErrRouteAuthor) {
		t.Fatal("route without an author policy", err)
	}
	for _, b := range []*BoundAuthor{{Nick: ""}, {Nick: "9x"}, {Nick: "a b"}, {Nick: "alerts", AccountTokenFile: "relative"}} {
		if validRouteAuthor(b, nil) == nil {
			t.Fatal("invalid bound author accepted", b)
		}
	}
	for _, p := range []string{"syslog_tls", "syslog_signed", "snmp_v3", "github_webhook", "chirpstack", "scp_inbox"} {
		if !RouteCarrier(p) || AuthorCarrier(p) {
			t.Fatal(p)
		}
	}
	for _, p := range []string{"mqtt", "amqp", "stomp"} {
		if !AuthorCarrier(p) || RouteCarrier(p) {
			t.Fatal(p)
		}
	}
}

func TestGitHubAndChirpStackAccountMaps(t *testing.T) {
	no := false
	target := core.Target{Kind: "room", Name: "#repo"}
	g := GitHubWebhook{SecretFile: "/tmp/secret", FreshnessSeconds: 60, Routes: []GitHubWebhookRoute{{RepositoryID: 1, Target: target}}}
	c := Defaults()
	if err := c.ValidateGitHubWebhook(g); err != nil {
		t.Fatal("guest-only webhook", err)
	}
	g.Accounts = []GitHubAccount{{Login: "octocat", AuthorID: 42, Nick: "octo", AccountTokenFile: "/tmp/octo.token"}}
	g.Guests = &no
	if err := c.ValidateGitHubWebhook(g); err != nil {
		t.Fatal("account-only webhook", err)
	}
	for name, mutate := range map[string]func(*GitHubWebhook){
		"no_author": func(g *GitHubWebhook) { g.Accounts = nil },
		"login":     func(g *GitHubWebhook) { g.Accounts[0].Login = "-bad" },
		"duplicate": func(g *GitHubWebhook) {
			g.Accounts = append(g.Accounts, GitHubAccount{Login: "OCTOCAT", Nick: "x"})
		},
		"nick":           func(g *GitHubWebhook) { g.Accounts[0].Nick = "" },
		"repository":     func(g *GitHubWebhook) { g.Routes = append(g.Routes, g.Routes[0]) },
		"target":         func(g *GitHubWebhook) { g.Routes[0].Target.Name = " repo " },
		"negative_id":    func(g *GitHubWebhook) { g.Accounts[0].AuthorID = -1 },
		"no_repository":  func(g *GitHubWebhook) { g.Routes = nil },
		"relative_token": func(g *GitHubWebhook) { g.Accounts[0].AccountTokenFile = "token" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := g
			copy.Accounts = append([]GitHubAccount(nil), g.Accounts...)
			copy.Routes = append([]GitHubWebhookRoute(nil), g.Routes...)
			mutate(&copy)
			if c.ValidateGitHubWebhook(copy) == nil {
				t.Fatal("invalid webhook accepted")
			}
		})
	}
	route := ChirpStackRoute{TenantID: "22222222-2222-2222-2222-222222222222", ApplicationID: "33333333-3333-3333-3333-333333333333", DevEUI: "70b3d57ed0000001", FPort: 1, Target: target}
	cs := ChirpStack{SecretFile: "/tmp/secret", ServerID: "11111111-1111-1111-1111-111111111111", FreshnessSeconds: 60, Routes: []ChirpStackRoute{route}}
	if err := c.ValidateChirpStack(cs); err != nil {
		t.Fatal("guest ChirpStack", err)
	}
	cs.Guests = &no
	if c.ValidateChirpStack(cs) == nil {
		t.Fatal("ChirpStack without guests or accounts")
	}
	cs.Accounts = []ChirpStackAccount{{DevEUI: "70b3d57ed0000001", Nick: "front-door"}}
	if err := c.ValidateChirpStack(cs); err != nil {
		t.Fatal(err)
	}
	cs.Accounts = append(cs.Accounts, cs.Accounts[0])
	if c.ValidateChirpStack(cs) == nil {
		t.Fatal("duplicate device account")
	}
}

// TestAccountMapJSON decodes the documented account entries with the strict
// configuration decoder, which does not flatten embedded structs.
func TestAccountMapJSON(t *testing.T) {
	var g GitHubWebhook
	if err := jsoninput.Decode([]byte(`{"accounts":[{"login":"octocat","author_id":583231,"nick":"octo","account_token_file":"/owner/octo.token"}],"guests":false}`), &g, MaxBytes); err != nil || g.Accounts[0].Author() != (BoundAuthor{Nick: "octo", AccountTokenFile: "/owner/octo.token"}) || g.GitHubGuests() {
		t.Fatal(g, err)
	}
	var c ChirpStack
	if err := jsoninput.Decode([]byte(`{"accounts":[{"dev_eui":"70b3d57ed0000001","nick":"front-door"}]}`), &c, MaxBytes); err != nil || c.Accounts[0].Author() != (BoundAuthor{Nick: "front-door"}) || !c.ChirpStackGuests() {
		t.Fatal(c, err)
	}
}

func TestSFTPAndSCPRouteAuthors(t *testing.T) {
	no := false
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "mailbox", "/tmp/mailbox-state"
	c.Upstream = &Upstream{Network: "unix", Address: "/tmp/native.sock"}
	c.SFTP = []SFTP{{ID: "box", Address: "127.0.0.1:22", Username: "mailbox", PrivateKeyFile: "/tmp/key", HostKeyFile: "/tmp/host", Root: "/srv/box", RemoteUID: 1000, Route: SFTPRoute{Target: core.Target{Kind: "room", Name: "#box"}, Operations: []core.Operation{core.Send}}}}
	if err := c.Validate(); err != nil {
		t.Fatal("guest SFTP mailbox", err)
	}
	c.SFTP[0].Route.Guests = &no
	if c.Validate() == nil {
		t.Fatal("SFTP without an author policy")
	}
	c.SFTP[0].Route.BoundAuthor = &BoundAuthor{Nick: "box-bot"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.SFTP = nil
	c.Listeners = []Listener{{ID: "scp", Protocol: "scp_inbox", Operations: []core.Operation{core.Send}, SCPInbox: &SCPInbox{LoginUID: 1000, Target: core.Target{Kind: "room", Name: "#box"}}}}
	if err := c.Validate(); err != nil {
		t.Fatal("guest SCP inbox", err)
	}
	c.Listeners[0].SCPInbox.Guests = &no
	if c.Validate() == nil {
		t.Fatal("SCP inbox without an author policy")
	}
}
