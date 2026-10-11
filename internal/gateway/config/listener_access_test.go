package config

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestListenerAccessDefaultsAndBounds(t *testing.T) {
	c, err := Parse([]byte(strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"web","protocol":"http","enabled":true,"address":"0.0.0.0:8080","operations":["send"]}]`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	l := c.Listeners[0]
	if !l.GuestsAllowed() || l.PasswordFile != "" || len(l.Targets) != 0 {
		t.Fatal("zero config must admit guests without a password or target list", l)
	}
	if got := l.AccessLimits(); got != (ListenerLimits{RequestsPerMinute: 120, AuthorRequestsPerMinute: 60, Connections: 256, ConnectionsPerSource: 16}) {
		t.Fatal("default limits", got)
	}
	c, err = Parse([]byte(strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"web","protocol":"websocket","guests":false,"password_file":"/private/pass","targets":[{"kind":"room","name":"#ops"},{"kind":"direct","name":"Alice"}],"limits":{"requests_per_minute":10,"connections":4}}]`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	l = c.Listeners[0]
	if l.GuestsAllowed() || l.Targets[1] != (core.Target{Kind: "direct", Name: "alice"}) || l.AccessLimits() != (ListenerLimits{RequestsPerMinute: 10, AuthorRequestsPerMinute: 60, Connections: 4, ConnectionsPerSource: 4}) {
		t.Fatal("configured access", l, l.AccessLimits())
	}
	for _, listener := range []string{
		`{"id":"x","protocol":"http","password_file":"relative"}`,
		`{"id":"x","protocol":"http","targets":[{"kind":"room","name":"bad"}]}`,
		`{"id":"x","protocol":"http","targets":[{"kind":"room","name":"#a"},{"kind":"room","name":"#a"}]}`,
		`{"id":"x","protocol":"http","limits":{"requests_per_minute":-1}}`,
		`{"id":"x","protocol":"http","limits":{"connections":2,"connections_per_source":3}}`,
		`{"id":"x","protocol":"http","limits":{"unknown":1}}`,
		`{"id":"x","protocol":"http3","address":"0.0.0.0:9000"}`,
	} {
		if _, err := Parse([]byte(strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[`+listener+`]`, 1))); err == nil {
			t.Errorf("accepted %s", listener)
		}
	}
	for _, protocol := range []string{"http", "websocket", "mcp_http", "framed"} {
		if _, err := Parse([]byte(strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"`+protocol+`","address":"192.0.2.7:9000"}]`, 1))); err != nil {
			t.Errorf("%s: plaintext off loopback rejected: %v", protocol, err)
		}
	}
}

func TestUpstreamConfiguration(t *testing.T) {
	valid := strings.Replace(emptyConfig, `"bindings":[]`, `"upstream":{"network":"unix","address":"/private/airc.sock","gateway_token_file":"/private/gateway.token"},"bindings":[]`, 1)
	c, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	b, ok := c.AuthorUpstream()
	if !ok || b.Network != "unix" || b.Address != "/private/airc.sock" || b.GatewayTokenFile != "/private/gateway.token" {
		t.Fatal("explicit upstream", b, ok)
	}
	for _, bad := range []string{
		`"upstream":{"network":"unix","address":"relative"}`,
		`"upstream":{"network":"tcp","address":"192.0.2.1:6697"}`,
		`"upstream":{"network":"tcp","address":"192.0.2.1:6697","tls_server_name":"airc"}`,
		`"upstream":{"network":"unix","address":"/s","gateway_token_file":"/t","access_token_file":"/t"}`,
		`"upstream":{"network":"udp","address":"127.0.0.1:1"}`,
	} {
		if _, err := Parse([]byte(strings.Replace(emptyConfig, `"bindings":[]`, bad+`,"bindings":[]`, 1))); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := Parse([]byte(strings.Replace(emptyConfig, `"bindings":[]`, `"bindings":[{"id":"b","network":"unix","address":"/s","account":"a","account_token_file":"/a","gateway_token_file":"/g"}]`, 1))); err == nil {
		t.Error("a binding configured a gateway token")
	}
}
