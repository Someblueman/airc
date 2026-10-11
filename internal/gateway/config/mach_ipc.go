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
	UID *uint32 `json:"uid"`
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
	for _, listener := range c.Listeners {
		if listener.Protocol != "mach_ipc" {
			if listener.MachIPC != nil {
				return errors.New("mach_ipc options require a mach_ipc listener")
			}
			continue
		}
		if err := validateMachIPCAuthor(listener); err != nil {
			return err
		}
	}
	return nil
}

// validateMachIPCAuthor checks a mach_ipc listener that takes plain author
// requests: its callers are a kernel-audited UID allowlist (none means the
// gateway's own UID), and replies are plain contract JSON on the reply port.
func validateMachIPCAuthor(listener Listener) error {
	if !machServiceName(listener.Address) {
		return errors.New("mach_ipc listener requires a valid bootstrap service name")
	}
	if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 {
		return errors.New("mach_ipc cannot use TLS or origins")
	}
	if listener.Enabled && !MachIPCAvailable() {
		return errors.New("mach_ipc requires Darwin ARM64")
	}
	if listener.MachIPC == nil {
		return nil
	}
	if len(listener.MachIPC.Callers) > 1024 {
		return errors.New("mach_ipc caller count is outside limits")
	}
	seen := map[uint32]bool{}
	for _, caller := range listener.MachIPC.Callers {
		if caller.UID == nil || *caller.UID == ^uint32(0) || seen[*caller.UID] {
			return errors.New("mach_ipc caller UID is missing, invalid or duplicated")
		}
		seen[*caller.UID] = true
	}
	return nil
}
