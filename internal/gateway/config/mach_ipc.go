package config

import (
	"errors"
	"runtime"
)

// MachIPCAvailable limits Mach service listeners to the native Darwin ARM64
// runtime used for qualification.
func MachIPCAvailable() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

type MachIPC struct {
	Callers []MachIPCCaller `json:"callers"`
}

type MachIPCCaller struct {
	UID       *uint32 `json:"uid"`
	Principal string  `json:"principal"`
}

func machServiceName(name string) bool {
	if len(name) < 1 || len(name) > 127 || !asciiAlphaNum(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		b := name[i]
		if !asciiAlphaNum(b) && b != '.' && b != '-' && b != '_' {
			return false
		}
	}
	return true
}

func asciiAlphaNum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func (c Config) validateMachIPC() error {
	listeners := make(map[string]Listener)
	principals := make(map[string]Principal, len(c.Principals))
	for _, p := range c.Principals {
		principals[p.ID] = p
	}
	for _, listener := range c.Listeners {
		if listener.Protocol != "mach_ipc" {
			if listener.MachIPC != nil {
				return errors.New("mach_ipc options require a mach_ipc listener")
			}
			continue
		}
		if listener.MachIPC == nil || !machServiceName(listener.Address) {
			return errors.New("mach_ipc listener requires a valid bootstrap service name and caller map")
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("mach_ipc cannot use TLS, origins or anonymous access")
		}
		if listener.Enabled && !MachIPCAvailable() {
			return errors.New("mach_ipc requires Darwin ARM64")
		}
		callers := listener.MachIPC.Callers
		if len(callers) < 1 || len(callers) > 1024 {
			return errors.New("mach_ipc caller count is outside limits")
		}
		type callerPair struct {
			uid       uint32
			principal string
		}
		pairs := make(map[callerPair]bool, len(callers))
		for _, caller := range callers {
			if caller.UID == nil || *caller.UID == ^uint32(0) {
				return errors.New("mach_ipc caller UID is missing or invalid")
			}
			principal, ok := principals[caller.Principal]
			if !ok || len(principal.SigningKeys) == 0 {
				return errors.New("mach_ipc caller must map to a configured signed principal")
			}
			pair := callerPair{uid: *caller.UID, principal: caller.Principal}
			if pairs[pair] {
				return errors.New("mach_ipc caller UID and principal pair is duplicated")
			}
			pairs[pair] = true
		}
		listeners[listener.ID] = listener
	}

	routeCounts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		listener, exists := listeners[route.Listener]
		if route.Protocol == "mach_ipc" || exists {
			if !exists || route.Protocol != "mach_ipc" || listener.Protocol != route.Protocol || route.Destination != "reply_port" || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
				return errors.New("invalid protected mach_ipc reply route")
			}
			mapped := false
			for _, caller := range listener.MachIPC.Callers {
				if caller.Principal == route.Principal {
					mapped = true
					break
				}
			}
			if !mapped {
				return errors.New("mach_ipc route principal has no caller mapping")
			}
			routeCounts[route.Listener+"\x00"+route.Principal]++
		}
	}
	for _, listener := range listeners {
		for _, caller := range listener.MachIPC.Callers {
			if routeCounts[listener.ID+"\x00"+caller.Principal] != 1 {
				return errors.New("mach_ipc requires exactly one protected reply route per mapped principal")
			}
		}
		if !listener.Enabled {
			continue
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 {
			return errors.New("enabled mach_ipc requires encrypted ingress keys and a response signing key")
		}
	}
	return nil
}
