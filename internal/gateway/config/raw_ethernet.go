package config

import (
	"errors"
	"net"
	"runtime"
	"strconv"
	"strings"
)

// RawEthernet selects the local interface used for raw Ethernet frames.
type RawEthernet struct {
	Interface string `json:"interface"`
}

// RawEthernetAvailable reports whether a raw Ethernet implementation exists.
func RawEthernetAvailable() bool { return runtime.GOOS == "linux" }

// ParseRawEthernetEndpoint parses a canonical lowercase MAC/channel endpoint.
func ParseRawEthernetEndpoint(endpoint string) (net.HardwareAddr, uint16, error) {
	if strings.Count(endpoint, "/") != 1 {
		return nil, 0, errors.New("invalid raw Ethernet endpoint")
	}
	parts := strings.SplitN(endpoint, "/", 2)
	address, err := net.ParseMAC(parts[0])
	channel, channelErr := strconv.ParseUint(parts[1], 10, 16)
	if err != nil || len(address) != 6 || address.String() != parts[0] || channelErr != nil || channel == 0 || strconv.FormatUint(channel, 10) != parts[1] {
		return nil, 0, errors.New("raw Ethernet endpoint requires a canonical six-byte MAC and channel")
	}
	var allZero, allBroadcast byte
	for _, octet := range address {
		allZero |= octet
		allBroadcast |= ^octet
	}
	if allZero == 0 || allBroadcast == 0 || address[0]&1 != 0 {
		return nil, 0, errors.New("raw Ethernet endpoint requires a nonzero unicast MAC")
	}
	return address, uint16(channel), nil
}

func (options *RawEthernet) Validate(endpoint string) error {
	if options == nil || options.Interface == "" || len(options.Interface) > 64 || strings.TrimSpace(options.Interface) != options.Interface || strings.ContainsAny(options.Interface, "/\\\x00\r\n\t ") {
		return errors.New("invalid raw Ethernet interface")
	}
	_, _, err := ParseRawEthernetEndpoint(endpoint)
	return err
}

func (c Config) validateRawEthernet() error {
	listeners := make(map[string]Listener)
	endpoints := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "raw_ethernet" {
			if listener.RawEthernet != nil {
				return errors.New("raw Ethernet options require a raw_ethernet listener")
			}
			continue
		}
		if err := listener.RawEthernet.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("raw_ethernet cannot use TLS, origins or anonymous access")
		}
		key := listener.RawEthernet.Interface + "\x00" + listener.Address
		if listener.Enabled && endpoints[key] {
			return errors.New("duplicate enabled raw_ethernet interface and endpoint")
		}
		if listener.Enabled {
			endpoints[key] = true
		}
		listeners[listener.ID] = listener
	}

	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		listener, exists := listeners[route.Listener]
		if route.Protocol != "raw_ethernet" && !exists {
			continue
		}
		if !exists || route.Protocol != "raw_ethernet" || listener.Protocol != "raw_ethernet" || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid protected raw_ethernet return route")
		}
		routeAddress, routeChannel, err := ParseRawEthernetEndpoint(route.Destination)
		if err != nil {
			return errors.New("raw_ethernet return route requires a canonical unicast endpoint")
		}
		sourceAddress, sourceChannel, _ := ParseRawEthernetEndpoint(listener.Address)
		if routeAddress.String() == sourceAddress.String() && routeChannel == sourceChannel {
			return errors.New("raw_ethernet return route cannot target the listener endpoint")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !RawEthernetAvailable() {
			return errors.New("raw_ethernet listener requires Linux")
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("raw_ethernet requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("raw_ethernet requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
