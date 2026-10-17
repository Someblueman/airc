package config

import (
	"strings"
	"testing"
	"time"
)

func TestKeepaliveListenersAndBounds(t *testing.T) {
	listener := `"listeners":[{"id":"ws","protocol":"websocket","enabled":true,"operations":["send"]KEEP}]`
	parse := func(protocol, keepalive string) (Config, error) {
		l := strings.Replace(listener, "KEEP", keepalive, 1)
		l = strings.Replace(l, `"websocket"`, `"`+protocol+`"`, 1)
		if protocol != "websocket" {
			l = strings.Replace(l, `"enabled":true`, `"enabled":true,"tls_cert_file":"/private/gateway/cert.pem","tls_key_file":"/private/gateway/key.pem"`, 1)
		}
		return Parse([]byte(strings.Replace(emptyConfig, `"listeners":[]`, l, 1)))
	}
	for _, protocol := range []string{"websocket", "http3", "webtransport", "rawquic", "rawquic_datagrams"} {
		c, err := parse(protocol, `,"keepalive":{"idle_seconds":120,"ping_seconds":20}`)
		if err != nil {
			t.Fatal(protocol, err)
		}
		if idle, ping := c.Listeners[0].KeepaliveTiming(); idle != 2*time.Minute || ping != 20*time.Second {
			t.Fatal(protocol, idle, ping)
		}
	}
	c, err := parse("websocket", "")
	if err != nil {
		t.Fatal(err)
	}
	if idle, ping := c.Listeners[0].KeepaliveTiming(); idle != 0 || ping != 0 {
		t.Fatal("defaults", idle, ping)
	}
	// Either field alone keeps the other's default.
	for _, k := range []string{`{"idle_seconds":5}`, `{"ping_seconds":600}`, `{"idle_seconds":3600,"ping_seconds":600}`} {
		if _, err := parse("websocket", `,"keepalive":`+k); err != nil {
			t.Fatal(k, err)
		}
	}
	for _, bad := range []struct{ protocol, keepalive string }{
		{"http", `{"idle_seconds":60}`},
		{"websocket", `{"idle_seconds":4}`},
		{"websocket", `{"idle_seconds":3601}`},
		{"websocket", `{"idle_seconds":-1}`},
		{"websocket", `{"ping_seconds":601}`},
		{"websocket", `{"ping_seconds":-1}`},
		{"websocket", `{"idle_seconds":30,"ping_seconds":30}`},
	} {
		if _, err := parse(bad.protocol, `,"keepalive":`+bad.keepalive); err == nil || !strings.Contains(err.Error(), "keepalive") {
			t.Fatal("accepted", bad, err)
		}
	}
}
