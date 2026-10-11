package config

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strconv"
)

// SCTPAvailable limits the initial mode to the native Linux ARM64 target used
// for socket qualification.
func SCTPAvailable() bool {
	return runtime.GOOS == "linux" && runtime.GOARCH == "arm64"
}

func sctpListenEndpoint(address string) error {
	host, port, err := net.SplitHostPort(address)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 0 || n > 65535 {
		return errors.New("invalid SCTP listener endpoint")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || ip.Is4In6() || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.MustParseAddr("255.255.255.255") {
		return errors.New("SCTP listener requires a literal unicast IP address")
	}
	return nil
}

func (c Config) validateSCTP() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "sctp" && listener.Protocol != "sctp_unordered" && listener.Protocol != "sctp_pr_ttl" {
			continue
		}
		if err := sctpListenEndpoint(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("SCTP cannot use TLS, origins or anonymous access")
		}
		if listener.Enabled && !SCTPAvailable() {
			return errors.New("SCTP requires Linux ARM64")
		}
		listeners[listener.ID] = listener
	}
	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		if route.Protocol != "sctp" && route.Protocol != "sctp_unordered" && route.Protocol != "sctp_pr_ttl" {
			continue
		}
		listener, ok := listeners[route.Listener]
		if !ok || listener.Protocol != route.Protocol || route.Listener == "" || route.Destination != "connection" || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid protected SCTP connection route")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		// Without protected return routes the listener carries plain author
		// requests and replies to their source; no keys are needed.
		if !c.LegacyRoutes(listener.ID) {
			continue
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("enabled SCTP requires encrypted ingress keys, a response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("SCTP requires a signing principal with exactly one protected connection route")
			}
		}
	}
	return nil
}
