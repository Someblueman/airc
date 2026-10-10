package config

import (
	"errors"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// BLEGATT configures a BlueZ GATT endpoint and its authenticated peers.
type BLEGATT struct {
	AdapterAddress string        `json:"adapter_address"`
	Peers          []BLEGATTPeer `json:"peers"`
}

type BLEGATTPeer struct {
	Address                string      `json:"address"`
	AddressType            string      `json:"address_type"`
	Principal              string      `json:"principal"`
	Target                 core.Target `json:"target"`
	RecipientKeyID         string      `json:"recipient_key_id"`
	RecipientPublicKeyFile string      `json:"recipient_public_key_file"`
}

func BLEGATTAvailable() bool { return runtime.GOOS == "linux" }

func blueZAdapterPath(value string) bool {
	const prefix = "/org/bluez/hci"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	n := strings.TrimPrefix(value, prefix)
	if n == "" || (len(n) > 1 && n[0] == '0') {
		return false
	}
	parsed, err := strconv.ParseUint(n, 10, 16)
	return err == nil && strconv.FormatUint(parsed, 10) == n
}

// Validate checks the BlueZ adapter path and the configured peer identities.
func (options *BLEGATT) Validate(adapterPath string) error {
	if options == nil || !blueZAdapterPath(adapterPath) {
		return errors.New("ble_gatt requires a canonical BlueZ adapter object path")
	}
	if _, err := ParseBluetoothAddress(options.AdapterAddress); err != nil {
		return errors.New("ble_gatt requires a canonical adapter Bluetooth address")
	}
	if len(options.Peers) < 1 || len(options.Peers) > 16 {
		return errors.New("ble_gatt peer count is outside limits")
	}
	local, _ := ParseBluetoothAddress(options.AdapterAddress)
	seen := make(map[[6]byte]bool, len(options.Peers))
	for _, peer := range options.Peers {
		address, err := ParseBluetoothAddress(peer.Address)
		if err != nil || address == local || seen[address] {
			return errors.New("ble_gatt peer address is invalid, local or duplicated")
		}
		if peer.AddressType != "public" && peer.AddressType != "random" {
			return errors.New("ble_gatt peer address_type must be public or random")
		}
		seen[address] = true
	}
	return nil
}

func (c Config) validateBLEGATT() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "ble_gatt" {
			if listener.BLEGATT != nil {
				return errors.New("ble_gatt options require a ble_gatt listener")
			}
			continue
		}
		if err := listener.BLEGATT.Validate(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("ble_gatt cannot use TLS, origins or anonymous access")
		}
		if len(listener.Operations) == 0 || operations(listener.Operations) != nil {
			return errors.New("ble_gatt requires explicit supported operations")
		}
		for _, op := range listener.Operations {
			if op != core.Send && op != core.ContextRead && op != core.HistoryRead && op != core.Search {
				return errors.New("ble_gatt supports only send, context.read, history.read and search")
			}
		}
		for _, peer := range listener.BLEGATT.Peers {
			if !id(peer.Principal) || !id(peer.RecipientKeyID) || !absolute(peer.RecipientPublicKeyFile) {
				return errors.New("ble_gatt peer requires a principal and response recipient key")
			}
			target := peer.Target
			if err := target.Normalize(); err != nil || target != peer.Target {
				return errors.New("ble_gatt peer target must be canonical")
			}
			principalFound := false
			for _, principal := range c.Principals {
				if principal.ID != peer.Principal {
					continue
				}
				if principal.Revoked || len(principal.SigningKeys) == 0 {
					return errors.New("ble_gatt peer principal must be active and signed")
				}
				for _, op := range listener.Operations {
					if !slices.Contains(principal.Operations, op) {
						return errors.New("ble_gatt principal lacks a configured operation grant")
					}
				}
				for _, granted := range principal.Targets {
					if err := granted.Normalize(); err == nil && granted == target {
						principalFound = true
						break
					}
				}
				break
			}
			if !principalFound {
				return errors.New("ble_gatt peer principal must grant its configured target")
			}
		}
		listeners[listener.ID] = listener
	}
	for _, route := range c.ReturnRoutes {
		if route.Protocol == "ble_gatt" {
			return errors.New("ble_gatt replies use the peer connection, not return routes")
		}
		if _, exists := listeners[route.Listener]; exists {
			return errors.New("ble_gatt listener cannot have a return route")
		}
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !BLEGATTAvailable() {
			return errors.New("enabled ble_gatt requires Linux")
		}
		if len(c.RecipientKeys) == 0 || c.ResponseSigningKey == nil {
			return errors.New("enabled ble_gatt requires ingress recipient keys and a response signer")
		}
	}
	return nil
}
