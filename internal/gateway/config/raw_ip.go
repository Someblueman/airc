package config

import (
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strconv"
	"strings"
)

// RawIP selects the local interface used for IPv4 raw-IP packet intake.
type RawIP struct {
	Interface string `json:"interface"`
}

// RawIPAvailable reports whether the raw-IP adapter has an implementation.
// Runtime availability does not imply platform qualification.
func RawIPAvailable() bool { return runtime.GOOS == "linux" }

// ParseRawIPEndpoint parses canonical IPv4:channel endpoints. The channel is
// an application demultiplexing identifier, not a UDP port.
func ParseRawIPEndpoint(endpoint string) (netip.Addr, uint16, error) {
	host, channelText, err := net.SplitHostPort(endpoint)
	if err != nil {
		return netip.Addr{}, 0, errors.New("invalid raw IP endpoint")
	}
	addr, err := netip.ParseAddr(host)
	channel, channelErr := strconv.ParseUint(channelText, 10, 16)
	if err != nil || channelErr != nil || !addr.Is4() || addr.IsUnspecified() || addr.IsMulticast() || addr == netip.MustParseAddr("255.255.255.255") || channel == 0 || strconv.FormatUint(channel, 10) != channelText || net.JoinHostPort(addr.String(), channelText) != endpoint {
		return netip.Addr{}, 0, errors.New("raw IP endpoint requires canonical IPv4 unicast and channel")
	}
	return addr, uint16(channel), nil
}

func (options *RawIP) Validate(endpoint string) error {
	if options == nil || options.Interface == "" || len(options.Interface) > 64 || strings.TrimSpace(options.Interface) != options.Interface || strings.ContainsAny(options.Interface, "/\\\x00\r\n\t ") {
		return errors.New("invalid raw IP interface")
	}
	_, _, err := ParseRawIPEndpoint(endpoint)
	return err
}

func (c Config) validateRawIP() error {
	listeners := make(map[string]Listener)
	endpoints := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "raw_ip" {
			if listener.RawIP != nil {
				return errors.New("raw IP options require a raw_ip listener")
			}
			continue
		}
		if err := listener.RawIP.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("raw_ip cannot use TLS, origins or anonymous access")
		}
		key := listener.RawIP.Interface + "\x00" + listener.Address
		if listener.Enabled && endpoints[key] {
			return errors.New("duplicate enabled raw_ip interface and endpoint")
		}
		if listener.Enabled {
			endpoints[key] = true
		}
		listeners[listener.ID] = listener
	}

	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		listener, exists := listeners[route.Listener]
		if route.Protocol != "raw_ip" && !exists {
			continue
		}
		if !exists || route.Protocol != "raw_ip" || listener.Protocol != "raw_ip" || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid protected raw_ip return route")
		}
		routeAddress, routeChannel, err := ParseRawIPEndpoint(route.Destination)
		if err != nil {
			return errors.New("raw_ip return route requires a canonical IPv4 unicast endpoint")
		}
		sourceAddress, sourceChannel, _ := ParseRawIPEndpoint(listener.Address)
		if sourceAddress == routeAddress && sourceChannel == routeChannel {
			return errors.New("raw_ip return route cannot target the listener endpoint")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !RawIPAvailable() {
			return errors.New("raw_ip listener requires Linux")
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("raw_ip requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("raw_ip requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
