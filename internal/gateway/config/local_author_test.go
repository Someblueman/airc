package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
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
		{Protocol: "fifo", Address: "/private/pair", Operations: send},
		{Protocol: "uart", Address: "/dev/tty-fixture", Operations: send},
		{Protocol: "file_mailbox", Address: "/private/mailbox", Operations: send, BoundAuthor: &BoundAuthor{Nick: "robot", AccountTokenFile: "/private/robot.token"}},
		{Protocol: "directory_mailbox", Address: "/private/dir", Operations: send, BoundAuthor: &BoundAuthor{Nick: "robot"}},
		{Protocol: "append_log", Address: "/private/log", Operations: send},
		{Protocol: "offline_bundle", Operations: send, BoundAuthor: &BoundAuthor{Nick: "courier"}},
		{Protocol: "bpv7", Operations: send, BundleProtocol: &BundleProtocol{Source: "dtn://producer/", Destination: "dtn://gateway/send", Target: core.Target{Kind: "room", Name: "#ops"}}},
	}
	if UnixAvailable() {
		listeners = append(listeners, Listener{Protocol: "unixgram", Address: "/private/gram.sock"}, Listener{Protocol: "system_v_queue", Address: "4242", ReplyTo: "4243"})
	}
	if UnixPacketAvailable() {
		listeners = append(listeners, Listener{Protocol: "unixpacket", Address: "/private/packet.sock"})
	}
	if POSIXQueueAvailable() {
		listeners = append(listeners, Listener{Protocol: "posix_mqueue", Address: "/airc-in", POSIXQueue: &POSIXQueue{Depth: 4}, ReplyTo: "/airc-out"})
	}
	if MachIPCAvailable() {
		uid := uint32(501)
		listeners = append(listeners, Listener{Protocol: "mach_ipc", Address: "org.airc.fixture"}, Listener{ID: "uids", Protocol: "mach_ipc", Address: "org.airc.uids", MachIPC: &MachIPC{Callers: []MachIPCCaller{{UID: &uid}}}})
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
		"bound author on a stream":  {Protocol: "stdio", Operations: []core.Operation{core.Send}, BoundAuthor: &BoundAuthor{Nick: "robot"}},
		"bound author bad nick":     {Protocol: "file_mailbox", Address: "/private/mailbox", Operations: []core.Operation{core.Send}, BoundAuthor: &BoundAuthor{Nick: "#robot"}},
		"bound author relative":     {Protocol: "file_mailbox", Address: "/private/mailbox", Operations: []core.Operation{core.Send}, BoundAuthor: &BoundAuthor{Nick: "robot", AccountTokenFile: "robot.token"}},
		"reply_to on a mailbox":     {Protocol: "file_mailbox", Address: "/private/mailbox", Operations: []core.Operation{core.Send}, ReplyTo: "/private/out"},
		"queue without reply_to":    {Protocol: "system_v_queue", Address: "4242"},
		"queue replying to itself":  {Protocol: "system_v_queue", Address: "4242", ReplyTo: "4242"},
		"queue reply of wrong kind": {Protocol: "system_v_queue", Address: "4242", ReplyTo: "/airc-out"},
		"bpv7 unnormalized target":  {Protocol: "bpv7", Operations: []core.Operation{core.Send}, BundleProtocol: &BundleProtocol{Source: "dtn://producer/", Destination: "dtn://gateway/send", Target: core.Target{Kind: "direct", Name: "Alice"}}},
	} {
		t.Run(name, func(t *testing.T) {
			c := localAuthorConfig(l)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
