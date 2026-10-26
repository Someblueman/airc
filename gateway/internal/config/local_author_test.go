package config

import (
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

// localAuthorConfig is one enabled local listener with no credentials,
// bindings, keys or routes: only an upstream.
func localAuthorConfig(l Listener) Config {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", "/private/state"
	c.Upstream = &Upstream{Network: "unix", Address: "/private/airc.sock"}
	l.Enabled = true
	if l.ID == "" {
		l.ID = "local"
	}
	c.Listeners = []Listener{l}
	return c
}

func TestLocalCarriersTakeRequestAuthors(t *testing.T) {
	send := []core.Operation{core.Send}
	listeners := []Listener{
		{Protocol: "stdio", Operations: send},
		{Protocol: "socketpair", Operations: send},
		{Protocol: "inherited", Operations: send},
		{Protocol: "file_mailbox", Address: "/private/mailbox", Operations: send, BoundAuthor: &BoundAuthor{Nick: "robot", AccountTokenFile: "/private/robot.token"}},
		{Protocol: "directory_mailbox", Address: "/private/dir", Operations: send, BoundAuthor: &BoundAuthor{Nick: "robot"}},
		{Protocol: "append_log", Address: "/private/log", Operations: send},
	}
	if Available("unix") {
		listeners = append(listeners, Listener{Protocol: "unixgram", Address: "/private/gram.sock"})
	}
	if Available("unixpacket") {
		listeners = append(listeners, Listener{Protocol: "unixpacket", Address: "/private/packet.sock"})
	}
	for _, l := range listeners {
		t.Run(l.Protocol, func(t *testing.T) {
			c := localAuthorConfig(l)
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			if !AuthorCarrier(l.Protocol) {
				t.Fatal("not an author carrier")
			}
		})
	}
}

func TestLocalAuthorOptionsAreBounded(t *testing.T) {
	for name, l := range map[string]Listener{
		"bound author on a stream": {Protocol: "stdio", Operations: []core.Operation{core.Send}, BoundAuthor: &BoundAuthor{Nick: "robot"}},
		"bound author bad nick":    {Protocol: "file_mailbox", Address: "/private/mailbox", Operations: []core.Operation{core.Send}, BoundAuthor: &BoundAuthor{Nick: "#robot"}},
		"bound author relative":    {Protocol: "file_mailbox", Address: "/private/mailbox", Operations: []core.Operation{core.Send}, BoundAuthor: &BoundAuthor{Nick: "robot", AccountTokenFile: "robot.token"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := localAuthorConfig(l)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
