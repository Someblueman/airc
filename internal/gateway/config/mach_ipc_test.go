package config

import (
	"encoding/json"
	"testing"
)

func machIPCConfig(enabled bool) Config {
	c := udpConfig(1)
	c.Listeners = []Listener{{
		ID: "mach", Protocol: "mach_ipc", Address: "com.example.airc.gateway",
		Enabled: enabled, MachIPC: &MachIPC{Callers: []MachIPCCaller{{UID: uint32Ptr(501), Principal: "a"}}},
	}}
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress.key"}}
	c.ReturnRoutes = []ReturnRoute{{
		ID: "mach-reply", Principal: "a", Protocol: "mach_ipc", Listener: "mach",
		Destination: "reply_port", RecipientKeyID: "reply", RecipientPublicKeyFile: "/public/reply",
	}}
	return c
}

func uint32Ptr(value uint32) *uint32 { return &value }

func TestMachIPCConfigValidatesCallerMapAndProtectedRoutes(t *testing.T) {
	c := machIPCConfig(false)
	if err := c.Validate(); err != nil {
		t.Fatalf("valid disabled Mach IPC config rejected: %v", err)
	}
	c = machIPCConfig(true)
	err := c.Validate()
	if MachIPCAvailable() && err != nil {
		t.Fatalf("enabled Mach IPC config rejected on Darwin ARM64: %v", err)
	}
	if !MachIPCAvailable() && err == nil {
		t.Fatal("enabled Mach IPC accepted on an unqualified target")
	}
}

func TestMachIPCConfigDoesNotRequireRoutesForUnmappedPrincipals(t *testing.T) {
	c := machIPCConfig(false)
	unmapped := c.Principals[0]
	unmapped.ID = "unmapped"
	unmapped.SigningKeys = []SigningKey{{ID: "sign-unmapped", PublicKeyFile: "/public/unmapped"}}
	c.Principals = append(c.Principals, unmapped)
	if err := c.Validate(); err != nil {
		t.Fatalf("unmapped principal incorrectly required a Mach IPC reply route: %v", err)
	}
}

func TestMachIPCConfigAllowsMultipleSignedPrincipalsForOneUID(t *testing.T) {
	c := machIPCConfig(false)
	second := c.Principals[0]
	second.ID = "second"
	second.SigningKeys = []SigningKey{{ID: "sign-second", PublicKeyFile: "/public/second"}}
	c.Principals = append(c.Principals, second)
	c.Listeners[0].MachIPC.Callers = append(c.Listeners[0].MachIPC.Callers, MachIPCCaller{UID: uint32Ptr(501), Principal: "second"})
	route := c.ReturnRoutes[0]
	route.ID, route.Principal = "mach-reply-second", "second"
	c.ReturnRoutes = append(c.ReturnRoutes, route)
	if err := c.Validate(); err != nil {
		t.Fatalf("same UID with a separately authenticated principal rejected: %v", err)
	}
}

func TestMachIPCConfigRejectsCallerAndRouteAmbiguity(t *testing.T) {
	tests := map[string]func(*Config){
		"omitted UID": func(c *Config) {
			c.Listeners[0].MachIPC.Callers[0].UID = nil
		},
		"invalid UID sentinel": func(c *Config) {
			c.Listeners[0].MachIPC.Callers[0].UID = uint32Ptr(^uint32(0))
		},
		"duplicate caller pair": func(c *Config) {
			c.Listeners[0].MachIPC.Callers = append(c.Listeners[0].MachIPC.Callers, MachIPCCaller{UID: uint32Ptr(501), Principal: "a"})
		},
		"unknown principal": func(c *Config) {
			c.Listeners[0].MachIPC.Callers[0].Principal = "missing"
		},
		"unsigned principal": func(c *Config) {
			c.Principals[0].SigningKeys = nil
		},
		"route protocol mismatch": func(c *Config) {
			c.ReturnRoutes[0].Protocol = "sctp"
		},
		"wrong listener": func(c *Config) {
			c.ReturnRoutes[0].Listener = "missing"
		},
		"wrong destination": func(c *Config) {
			c.ReturnRoutes[0].Destination = "com.example.reply"
		},
		"unmapped route principal": func(c *Config) {
			principal := c.Principals[0]
			principal.ID = "b"
			principal.SigningKeys = []SigningKey{{ID: "sign-b", PublicKeyFile: "/public/b"}}
			c.Principals = append(c.Principals, principal)
			route := c.ReturnRoutes[0]
			route.ID, route.Principal = "reply-b", "b"
			c.ReturnRoutes = append(c.ReturnRoutes, route)
		},
		"duplicate route": func(c *Config) {
			route := c.ReturnRoutes[0]
			route.ID = "duplicate"
			c.ReturnRoutes = append(c.ReturnRoutes, route)
		},
		"option on other protocol": func(c *Config) {
			c.Listeners[0].Protocol = "unix"
		},
		"missing mach option": func(c *Config) {
			c.Listeners[0].MachIPC = nil
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
	if err := json.Unmarshal([]byte(`{"principal":"a"}`), &caller); err != nil {
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
