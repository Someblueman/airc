package config

import (
	"strings"
	"testing"
)

func TestMCPHTTPUsesNetworkAndReadPermissionBoundaries(t *testing.T) {
	input := strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"mcp","protocol":"mcp_http","enabled":true,"operations":["send","receipt.get"]}]`, 1)
	c, err := Parse([]byte(input))
	if err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal("MCP loopback configuration", c, err)
	}
	for _, bad := range []string{
		strings.Replace(input, `"receipt.get"`, `"watch"`, 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("MCP bypassed network or cursor boundary")
		}
	}
	if _, err := Parse([]byte(strings.Replace(input, `"enabled":true`, `"enabled":true,"address":"0.0.0.0:9000"`, 1))); err != nil {
		t.Fatal("plaintext MCP off loopback rejected", err)
	}
	watch := strings.Replace(input, `"receipt.get"`, `"watch"`, 1)
	watch = strings.Replace(watch, `"version":1`, `"version":1,"cursor_key_file":"/private/gateway/cursor.key"`, 1)
	if _, err := Parse([]byte(watch)); err != nil {
		t.Fatal("MCP watch configuration rejected", err)
	}
	watch = strings.Replace(watch, `"version":1`, `"version":1,"limits":{"sessions_per_principal":1,"watches_per_principal":0}`, 1)
	if _, err := Parse([]byte(watch)); err == nil {
		t.Fatal("MCP watch admitted without read reserve")
	}
}
