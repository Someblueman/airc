package config

import "testing"

func TestExactBrowserOrigins(t *testing.T) {
	for _, origin := range []string{"https://example.com", "https://example.com:8443", "http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080", "https://[::ffff:7f00:1]", "https://[2001:db8::1]:8443"} {
		if err := ValidateOrigins([]string{origin}); err != nil {
			t.Fatal(origin, err)
		}
	}
	for _, origin := range []string{"", "*", "null", "https://example.com/", "https://user@example.com", "https://example.com?", "https://example.com#", "https://EXAMPLE.com", "https://example.com:443", "http://localhost:80", "http://example.com", "https://example.com:0", "https://example.com:65536", "https://example.com:080", "https://example.com:", "https://*.example.com", "https://éxample.com", "https://[0:0:0:0:0:0:0:1]", "https://[invalid]", "https://127.1", "https://127.000.0.1", "https://0x7f000001", "https://2130706433"} {
		if err := ValidateOrigins([]string{origin}); err == nil {
			t.Fatal("accepted noncanonical origin", origin)
		}
	}
	if ValidateOrigins([]string{"https://example.com", "https://example.com"}) == nil {
		t.Fatal("duplicate origin accepted")
	}
}

func TestListenerOriginConfiguration(t *testing.T) {
	for _, protocol := range []string{"http", "websocket", "mcp_http", "framed", "future"} {
		c := Defaults()
		c.Version = 1
		c.Realm = "fixture"
		c.StateDir = "/tmp/fixture-state"
		c.Listeners = []Listener{{ID: "fixture", Protocol: protocol, AllowedOrigins: []string{"https://example.com"}}}
		err := c.Validate()
		allowed := protocol == "http" || protocol == "websocket" || protocol == "mcp_http"
		if allowed && err != nil || !allowed && err == nil {
			t.Fatal("origin policy allowed on wrong listener", protocol, err)
		}
	}
}
