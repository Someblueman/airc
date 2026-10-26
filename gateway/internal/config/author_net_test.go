package config

import "testing"

// The network carriers take plain author requests: an address is enough.
func TestAuthorNetworkCarriersNeedNoKeys(t *testing.T) {
	for _, protocol := range []string{"udp", "coap_udp", "zeromq", "nng"} {
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
