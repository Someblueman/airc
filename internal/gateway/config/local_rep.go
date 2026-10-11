package config

import "fmt"

// validateLocalREP checks a ZeroMQ or NNG REQ/REP listener. Requests are plain
// contract JSON run as their author.
func validateLocalREP(l Listener) error {
	if l.Protocol != "zeromq" && l.Protocol != "nng" {
		return nil
	}
	name := "ZeroMQ"
	if l.Protocol == "nng" {
		name = "NNG"
	}
	if l.TLSKeyFile != "" || l.TLSCertFile != "" {
		return fmt.Errorf("%s profile cannot use TLS", name)
	}
	// Like IRC, the plain profile needs no TLS, even off loopback; account
	// tokens in request bodies are then visible on the network.
	return endpoint(l.Address, true)
}
