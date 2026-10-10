package config

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
)

// L2CAP restricts accepted peers for one Bluetooth BR/EDR PSM.
type L2CAP struct {
	AllowedPeerAddresses []string `json:"allowed_peer_addresses"`
}

// L2CAPAvailable reports whether the native listener implementation exists.
func L2CAPAvailable() bool { return runtime.GOOS == "linux" }

// ParseL2CAPEndpoint parses a canonical lowercase BD_ADDR/dynamic-PSM endpoint.
func ParseL2CAPEndpoint(endpoint string) ([6]byte, uint16, error) {
	if strings.Count(endpoint, "/") != 1 {
		return [6]byte{}, 0, errors.New("invalid Bluetooth L2CAP endpoint")
	}
	parts := strings.SplitN(endpoint, "/", 2)
	address, err := ParseBluetoothAddress(parts[0])
	psm, psmErr := strconv.ParseUint(parts[1], 10, 16)
	if err != nil || psmErr != nil || psm < 4097 || psm > 65535 || psm&0x0101 != 1 || strconv.FormatUint(psm, 10) != parts[1] {
		return [6]byte{}, 0, errors.New("bluetooth L2CAP endpoint requires a canonical dynamic BR/EDR PSM")
	}
	return address, uint16(psm), nil
}

func (options *L2CAP) Validate(endpoint string) error {
	if options == nil {
		return errors.New("missing Bluetooth L2CAP options")
	}
	local, _, err := ParseL2CAPEndpoint(endpoint)
	if err != nil {
		return err
	}
	if len(options.AllowedPeerAddresses) < 1 || len(options.AllowedPeerAddresses) > 64 {
		return errors.New("bluetooth L2CAP peer address count is outside limits")
	}
	seen := make(map[[6]byte]bool, len(options.AllowedPeerAddresses))
	for _, peerText := range options.AllowedPeerAddresses {
		peer, err := ParseBluetoothAddress(peerText)
		if err != nil || peer == local || seen[peer] {
			return errors.New("invalid, local or duplicate Bluetooth L2CAP peer address")
		}
		seen[peer] = true
	}
	return nil
}

func (c Config) validateL2CAP() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "bluetooth_l2cap" {
			if listener.L2CAP != nil {
				return errors.New("l2cap options require a bluetooth_l2cap listener")
			}
			continue
		}
		if err := listener.L2CAP.Validate(listener.Address); err != nil {
			return err
		}
		if (listener.TLSCertFile == "") != (listener.TLSKeyFile == "") {
			return errors.New("bluetooth L2CAP requires both TLS certificate and key")
		}
		if len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("bluetooth L2CAP cannot use origins or anonymous access")
		}
		listeners[listener.ID] = listener
	}
	for _, route := range c.ReturnRoutes {
		if route.Protocol == "bluetooth_l2cap" {
			return errors.New("bluetooth L2CAP uses its connection for responses, not return routes")
		}
		if _, exists := listeners[route.Listener]; exists {
			return errors.New("bluetooth L2CAP listener cannot have a return route")
		}
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !L2CAPAvailable() {
			return errors.New("bluetooth L2CAP requires Linux")
		}
		if listener.TLSCertFile == "" || listener.TLSKeyFile == "" {
			return errors.New("enabled Bluetooth L2CAP listener requires TLS certificate and key")
		}
		if len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("bluetooth L2CAP requires ingress recipient keys and signed principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 {
				return errors.New("bluetooth L2CAP requires signed principals")
			}
		}
	}
	return nil
}
