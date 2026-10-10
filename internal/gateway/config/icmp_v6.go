package config

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strconv"
	"strings"
)

// ICMPv6 selects one local interface for Echo request intake.
type ICMPv6 struct {
	Interface string `json:"interface"`
}

// ICMPv6Available reports whether this profile is qualified for runtime use.
func ICMPv6Available() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

// ParseICMPv6Endpoint parses canonical [IPv6]:channel endpoints. The channel
// is an Echo identifier, not a UDP port.
func ParseICMPv6Endpoint(endpoint string) (netip.Addr, uint16, error) {
	host, channelText, err := net.SplitHostPort(endpoint)
	if err != nil {
		return netip.Addr{}, 0, errors.New("invalid ICMPv6 endpoint")
	}
	addr, err := netip.ParseAddr(host)
	channel, channelErr := strconv.ParseUint(channelText, 10, 16)
	if err != nil || channelErr != nil || !addr.Is6() || addr.Is4In6() || addr.Zone() != "" || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || channel == 0 || strconv.FormatUint(channel, 10) != channelText || net.JoinHostPort(addr.String(), channelText) != endpoint {
		return netip.Addr{}, 0, errors.New("ICMPv6 endpoint requires a canonical IPv6 unicast address and Echo identifier")
	}
	return addr, uint16(channel), nil
}

func (options *ICMPv6) Validate(endpoint string) error {
	if options == nil || options.Interface == "" || len(options.Interface) > 64 || strings.TrimSpace(options.Interface) != options.Interface || strings.ContainsAny(options.Interface, "/\\\x00\r\n\t ") {
		return errors.New("invalid ICMPv6 interface")
	}
	_, _, err := ParseICMPv6Endpoint(endpoint)
	return err
}

func (c Config) validateICMPv6() error {
	listeners := make(map[string]Listener)
	endpoints := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "icmp_v6" {
			if listener.ICMPv6 != nil {
				return errors.New("ICMPv6 options require an ICMPv6 listener")
			}
			continue
		}
		if err := listener.ICMPv6.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("ICMPv6 cannot use TLS, origins or anonymous access")
		}
		key := listener.ICMPv6.Interface + "\x00" + listener.Address
		if listener.Enabled && endpoints[key] {
			return errors.New("duplicate enabled ICMPv6 interface and endpoint")
		}
		if listener.Enabled {
			endpoints[key] = true
		}
		listeners[listener.ID] = listener
	}

	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		listener, exists := listeners[route.Listener]
		if route.Protocol != "icmp_v6" && !exists {
			continue
		}
		if !exists || route.Protocol != "icmp_v6" || listener.Protocol != "icmp_v6" || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid protected ICMPv6 return route")
		}
		routeAddress, routeChannel, err := ParseICMPv6Endpoint(route.Destination)
		if err != nil {
			return errors.New("ICMPv6 return route requires a canonical IPv6 unicast endpoint")
		}
		sourceAddress, sourceChannel, _ := ParseICMPv6Endpoint(listener.Address)
		if sourceAddress == routeAddress && sourceChannel == routeChannel {
			return errors.New("ICMPv6 return route cannot target the listener endpoint")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !ICMPv6Available() {
			return errors.New("ICMPv6 listener requires Darwin ARM64")
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("ICMPv6 requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("ICMPv6 requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
