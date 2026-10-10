package config

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
)

// RFCOMM limits accepted peers for one Bluetooth serial channel.
type RFCOMM struct {
	AllowedPeerAddresses []string `json:"allowed_peer_addresses"`
}

// RFCOMMAvailable reports whether the native listener implementation exists.
func RFCOMMAvailable() bool { return runtime.GOOS == "linux" }

// ParseBluetoothAddress parses a canonical lowercase human-order BD_ADDR.
func ParseBluetoothAddress(value string) ([6]byte, error) {
	var address [6]byte
	if len(value) != 17 {
		return address, errors.New("invalid Bluetooth address")
	}
	for i := 0; i < 6; i++ {
		if i > 0 && value[i*3-1] != ':' {
			return [6]byte{}, errors.New("Bluetooth address must use lowercase colon-separated octets")
		}
		hi, okHi := bluetoothHex(value[i*3])
		lo, okLo := bluetoothHex(value[i*3+1])
		if !okHi || !okLo {
			return [6]byte{}, errors.New("Bluetooth address must use lowercase hexadecimal")
		}
		address[i] = hi<<4 | lo
	}
	allZero, allFF := true, true
	for _, octet := range address {
		allZero = allZero && octet == 0
		allFF = allFF && octet == 0xff
	}
	if allZero || allFF {
		return [6]byte{}, errors.New("Bluetooth address cannot be zero or broadcast")
	}
	return address, nil
}

func bluetoothHex(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	default:
		return 0, false
	}
}

// ParseRFCOMMEndpoint parses a canonical lowercase BD_ADDR/channel endpoint.
func ParseRFCOMMEndpoint(endpoint string) ([6]byte, uint8, error) {
	if strings.Count(endpoint, "/") != 1 {
		return [6]byte{}, 0, errors.New("invalid Bluetooth RFCOMM endpoint")
	}
	parts := strings.SplitN(endpoint, "/", 2)
	address, err := ParseBluetoothAddress(parts[0])
	channel, channelErr := strconv.ParseUint(parts[1], 10, 8)
	if err != nil || channelErr != nil || channel < 1 || channel > 30 || strconv.FormatUint(channel, 10) != parts[1] {
		return [6]byte{}, 0, errors.New("Bluetooth RFCOMM endpoint requires a canonical address and channel 1 through 30")
	}
	return address, uint8(channel), nil
}

func (options *RFCOMM) Validate(endpoint string) error {
	if options == nil {
		return errors.New("missing Bluetooth RFCOMM options")
	}
	local, _, err := ParseRFCOMMEndpoint(endpoint)
	if err != nil {
		return err
	}
	if len(options.AllowedPeerAddresses) < 1 || len(options.AllowedPeerAddresses) > 64 {
		return errors.New("Bluetooth RFCOMM peer address count is outside limits")
	}
	seen := make(map[[6]byte]bool, len(options.AllowedPeerAddresses))
	for _, peerText := range options.AllowedPeerAddresses {
		peer, err := ParseBluetoothAddress(peerText)
		if err != nil || peer == local || seen[peer] {
			return errors.New("invalid, local or duplicate Bluetooth RFCOMM peer address")
		}
		seen[peer] = true
	}
	return nil
}

func (c Config) validateRFCOMM() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "bluetooth_rfcomm" {
			if listener.RFCOMM != nil {
				return errors.New("rfcomm options require a bluetooth_rfcomm listener")
			}
			continue
		}
		if err := listener.RFCOMM.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" && listener.TLSKeyFile == "" || listener.TLSKeyFile != "" && listener.TLSCertFile == "" {
			return errors.New("Bluetooth RFCOMM requires both TLS certificate and key")
		}
		if len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("Bluetooth RFCOMM cannot use origins or anonymous access")
		}
		listeners[listener.ID] = listener
	}
	for _, route := range c.ReturnRoutes {
		if route.Protocol == "bluetooth_rfcomm" {
			return errors.New("Bluetooth RFCOMM uses its connection for responses, not return routes")
		}
		if _, exists := listeners[route.Listener]; exists {
			return errors.New("Bluetooth RFCOMM listener cannot have a return route")
		}
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !RFCOMMAvailable() {
			return errors.New("Bluetooth RFCOMM requires Linux")
		}
		if listener.TLSCertFile == "" || listener.TLSKeyFile == "" {
			return errors.New("enabled Bluetooth RFCOMM listener requires TLS certificate and key")
		}
		if len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("Bluetooth RFCOMM requires ingress recipient keys and signed principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 {
				return errors.New("Bluetooth RFCOMM requires signed principals")
			}
		}
	}
	return nil
}
