package config

import (
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/core"
)

type ChirpStack struct {
	SecretFile       string            `json:"secret_file"`
	ServerID         string            `json:"server_id"`
	FreshnessSeconds int64             `json:"freshness_seconds"`
	Routes           []ChirpStackRoute `json:"routes"`
}

type ChirpStackRoute struct {
	TenantID      string      `json:"tenant_id"`
	ApplicationID string      `json:"application_id"`
	DevEUI        string      `json:"dev_eui"`
	FPort         uint32      `json:"f_port"`
	Principal     string      `json:"principal"`
	Target        core.Target `json:"target"`
}

// ValidChirpStackUUID reports whether value is a canonical lowercase, nonzero UUID.
func ValidChirpStackUUID(value string) bool {
	if value == "00000000-0000-0000-0000-000000000000" || len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		b := value[i]
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func validDevEUI(value string) bool {
	if len(value) != 16 {
		return false
	}
	nonzero := false
	for i := range value {
		b := value[i]
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
		nonzero = nonzero || b != '0'
	}
	return nonzero
}

// ValidateChirpStack checks the configured provider identity and routes before
// an auth validator uses the profile.
func (c Config) ValidateChirpStack(options ChirpStack) error {
	if !absolute(options.SecretFile) || !ValidChirpStackUUID(options.ServerID) || options.FreshnessSeconds < 1 || options.FreshnessSeconds > 300 || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return errors.New("invalid ChirpStack secret, server ID, freshness or route count")
	}
	type routeKey struct {
		tenantID      string
		applicationID string
		devEUI        string
		fPort         uint32
	}
	seen := make(map[routeKey]bool, len(options.Routes))
	principals := make(map[string]Principal, len(c.Principals))
	for _, principal := range c.Principals {
		principals[principal.ID] = principal
	}
	for _, route := range options.Routes {
		key := routeKey{tenantID: route.TenantID, applicationID: route.ApplicationID, devEUI: route.DevEUI, fPort: route.FPort}
		if !ValidChirpStackUUID(route.TenantID) || !ValidChirpStackUUID(route.ApplicationID) || !validDevEUI(route.DevEUI) || route.FPort < 1 || route.FPort > 223 || !id(route.Principal) || seen[key] {
			return errors.New("invalid or duplicate ChirpStack device route")
		}
		seen[key] = true
		principal, exists := principals[route.Principal]
		if !exists || principal.Revoked || !slices.Contains(principal.Operations, core.Send) {
			return errors.New("ChirpStack route requires an active Send principal")
		}
		target := route.Target
		if err := target.Normalize(); err != nil || target != route.Target {
			return errors.New("ChirpStack route target must be canonical")
		}
		granted := false
		for _, candidate := range principal.Targets {
			if candidate.Normalize() == nil && candidate == target {
				granted = true
				break
			}
		}
		if !granted {
			return errors.New("ChirpStack route target is not granted to its principal")
		}
	}
	return nil
}

func (c Config) validateChirpStackListeners() error {
	listeners := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "chirpstack" {
			if listener.ChirpStack != nil {
				return errors.New("ChirpStack options require a chirpstack listener")
			}
			continue
		}
		if listener.ChirpStack == nil {
			return errors.New("chirpstack listener requires options")
		}
		if err := c.ValidateChirpStack(*listener.ChirpStack); err != nil {
			return err
		}
		if len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("chirpstack cannot use origins or anonymous access")
		}
		if len(listener.Operations) != 1 || listener.Operations[0] != core.Send {
			return errors.New("chirpstack supports only the Send operation")
		}
		listeners[listener.ID] = true
		if listener.Enabled && (listener.TLSCertFile == "" || listener.TLSKeyFile == "") {
			return errors.New("enabled chirpstack requires TLS certificate and key")
		}
	}
	for _, route := range c.ReturnRoutes {
		if route.Protocol == "chirpstack" || listeners[route.Listener] {
			return errors.New("chirpstack uses configured device routes, not return routes")
		}
	}
	return nil
}
