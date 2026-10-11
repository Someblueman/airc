package config

import (
	"encoding/json"
	"testing"
)

func machIPCConfig(enabled bool) Config {
	c := udpConfig()
	c.Listeners = []Listener{{
		ID: "mach", Protocol: "mach_ipc", Address: "com.example.airc.gateway",
		Enabled: enabled, MachIPC: &MachIPC{Callers: []MachIPCCaller{{UID: uint32Ptr(501)}}},
	}}
	return c
}

func uint32Ptr(value uint32) *uint32 { return &value }

func TestMachIPCConfigValidatesCallerAllowlist(t *testing.T) {
	c := machIPCConfig(false)
	if err := c.Validate(); err != nil {
		t.Fatalf("valid disabled Mach IPC config rejected: %v", err)
	}
	// Without a caller list the gateway's own UID is the only caller.
	c.Listeners[0].MachIPC = nil
	if err := c.Validate(); err != nil {
		t.Fatalf("Mach IPC without callers rejected: %v", err)
	}
	c = machIPCConfig(true)
	err := c.Validate()
	if Available("mach_ipc") && err != nil {
		t.Fatalf("enabled Mach IPC config rejected on Darwin ARM64: %v", err)
	}
	if !Available("mach_ipc") && err == nil {
		t.Fatal("enabled Mach IPC accepted on an unqualified target")
	}
}

func TestMachIPCConfigRejectsInvalidCallers(t *testing.T) {
	tests := map[string]func(*Config){
		"omitted UID": func(c *Config) {
			c.Listeners[0].MachIPC.Callers[0].UID = nil
		},
		"invalid UID sentinel": func(c *Config) {
			c.Listeners[0].MachIPC.Callers[0].UID = uint32Ptr(^uint32(0))
		},
		"duplicate caller": func(c *Config) {
			c.Listeners[0].MachIPC.Callers = append(c.Listeners[0].MachIPC.Callers, MachIPCCaller{UID: uint32Ptr(501)})
		},
		"option on other protocol": func(c *Config) {
			c.Listeners[0].Protocol = "unix"
		},
		"invalid service name": func(c *Config) {
			c.Listeners[0].Address = ".com.example"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := machIPCConfig(false)
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid Mach IPC config accepted")
			}
		})
	}
}

func TestMachIPCConfigCallerUIDIsRequiredWhenDecoded(t *testing.T) {
	var caller MachIPCCaller
	if err := json.Unmarshal([]byte(`{}`), &caller); err != nil {
		t.Fatal(err)
	}
	if caller.UID != nil {
		t.Fatalf("omitted UID decoded as %d", *caller.UID)
	}
	c := machIPCConfig(false)
	c.Listeners[0].MachIPC.Callers[0] = caller
	if err := c.Validate(); err == nil {
		t.Fatal("omitted caller UID accepted")
	}
}

func TestMachIPCServiceNameGrammar(t *testing.T) {
	for _, name := range []string{"A", "0service", "com.example.gateway", "com-example_gateway"} {
		if !machServiceName(name) {
			t.Errorf("valid service name rejected: %q", name)
		}
	}
	for _, name := range []string{"", ".service", "-service", "_service", "has space", "é", string(make([]byte, 128))} {
		if machServiceName(name) {
			t.Errorf("invalid service name accepted: %q", name)
		}
	}
}
