package config

import (
	"runtime"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// Without principals, keys or return routes the network carriers take plain
// author requests; return routes keep the legacy shim and its requirements.
func TestAuthorNetworkCarriersNeedNoKeys(t *testing.T) {
	strip := func(c *Config) {
		c.Principals, c.ReturnRoutes, c.ResponseSigningKey, c.RecipientKeys = nil, nil, nil, nil
	}
	cases := map[string]func() Config{
		"ipv4_multicast": multicastIPv4Config,
		"ipv6_multicast": multicastIPv6Config,
		"ipv4_broadcast": broadcastIPv4Config,
	}
	if runtime.GOOS == "linux" {
		cases["udp_lite"] = udpLiteConfig
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			c := build()
			strip(&c)
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			legacy := build()
			legacy.ResponseSigningKey = nil
			if err := legacy.Validate(); err == nil {
				t.Fatal("legacy routes accepted without their signing key")
			}
		})
	}
	for _, protocol := range []string{"udp", "coap_udp", "coap_tcp", "zeromq", "nng"} {
		c := udpConfig(0)
		strip(&c)
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
	c := udpConfig(0)
	c.ResponseSigningKey = nil
	c.Listeners[0] = Listener{ID: "sn", Protocol: "mqtt_sn", Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Send}, MQTTSN: &MQTTSN{Routes: []MQTTSNRoute{{TopicID: 1, Target: core.Target{Kind: "room", Name: "#Ops"}}}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].MQTTSN.Routes = append(c.Listeners[0].MQTTSN.Routes, MQTTSNRoute{Principal: "a", TopicID: 2, Target: core.Target{Kind: "room", Name: "#ops"}})
	if err := c.Validate(); err == nil {
		t.Fatal("mixed legacy and author topic routes accepted")
	}
	c.Listeners[0].MQTTSN.Routes = []MQTTSNRoute{{TopicID: 1, Target: core.Target{Kind: "room", Name: "bad"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("invalid topic target accepted")
	}

	o := udpConfig(0)
	o.ResponseSigningKey = nil
	o.Listeners[0] = Listener{ID: "oscore", Protocol: "coap_oscore", Address: "127.0.0.1:0", OSCORE: &OSCORE{Binary: "/opt/worker", ContextFile: "/private/context"}}
	if err := o.Validate(); err != nil {
		t.Fatal("OSCORE without a principal", err)
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
