package config

import (
	"runtime"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// The network carriers take plain author requests: an address (and the carrier's own options) is enough.
func TestAuthorNetworkCarriersNeedNoKeys(t *testing.T) {
	packet := func(protocol, address string, set func(*Listener)) func() Config {
		return func() Config {
			c := udpConfig()
			c.Listeners[0].Protocol, c.Listeners[0].Address = protocol, address
			set(&c.Listeners[0])
			return c
		}
	}
	cases := map[string]func() Config{
		"ipv4_multicast": packet("ipv4_multicast", "239.255.1.2:9001", func(l *Listener) { l.IPv4Multicast = &IPv4Multicast{Interface: "lo0"} }),
		"ipv6_multicast": packet("ipv6_multicast", "[ff12::1234]:9001", func(l *Listener) { l.IPv6Multicast = &IPv6Multicast{Interface: "lo0"} }),
		"ipv4_broadcast": packet("ipv4_broadcast", "192.0.2.255:9001", func(l *Listener) {
			l.IPv4Broadcast = &IPv4Broadcast{Interface: "lo0", Subnet: "192.0.2.0/24"}
		}),
	}
	if runtime.GOOS == "linux" {
		cases["udp_lite"] = packet("udp_lite", "127.0.0.1:9001", func(l *Listener) { l.UDPLite = &UDPLite{ChecksumCoverage: "full"} })
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			c := build()
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, protocol := range []string{"udp", "coap_udp", "coap_tcp", "zeromq", "nng"} {
		c := udpConfig()
		c.Listeners[0].Protocol = protocol
		c.Listeners[0].Address = "0.0.0.0:9001"
		c.Listeners[0].Operations = nil
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", protocol, err)
		}
		if !AuthorCarrier(protocol) {
			t.Errorf("%s is not an author carrier", protocol)
		}
	}
}

func TestAuthorMQTTSNTopicsAndOSCOREAuthor(t *testing.T) {
	c := udpConfig()
	c.Listeners[0] = Listener{ID: "sn", Protocol: "mqtt_sn", Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Send}, MQTTSN: &MQTTSN{Routes: []MQTTSNRoute{{TopicID: 1, Target: core.Target{Kind: "room", Name: "#Ops"}}}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].MQTTSN.Routes = []MQTTSNRoute{{TopicID: 1, Target: core.Target{Kind: "room", Name: "bad"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("invalid topic target accepted")
	}

	o := udpConfig()
	o.Listeners[0] = Listener{ID: "oscore", Protocol: "coap_oscore", Address: "127.0.0.1:0", OSCORE: &OSCORE{Binary: "/opt/worker", ContextFile: "/private/context"}}
	if err := o.Validate(); err != nil {
		t.Fatal("OSCORE listener", err)
	}
	o.Listeners[0].OSCORE.Author = &OSCOREAuthor{Nick: "sensor", TokenFile: "/private/token"}
	if err := o.Validate(); err != nil {
		t.Fatal("OSCORE default author", err)
	}
	for _, bad := range []OSCOREAuthor{{Nick: "#bad"}, {Nick: "sensor", TokenFile: "relative"}} {
		o.Listeners[0].OSCORE.Author = &bad
		if err := o.Validate(); err == nil {
			t.Fatalf("accepted OSCORE author %+v", bad)
		}
	}
}
