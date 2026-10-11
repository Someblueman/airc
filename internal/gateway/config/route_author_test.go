package config

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
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
	for _, p := range []string{"syslog_tls", "syslog_signed", "snmp_v3", "scp_inbox"} {
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
