package config

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/testtls"
)

// A UDP listener may share a TCP listener's port, as HTTP/3 shares HTTPS's;
// two listeners on the same family still may not.
func TestUDPAndTCPListenersMayShareAPort(t *testing.T) {
	cert := testtls.New(t)
	h3 := fmt.Sprintf(`{"id":"h3","protocol":"http3","enabled":true,"address":"127.0.0.1:9443","tls_cert_file":%q,"tls_key_file":%q}`, cert.CertPath, cert.KeyPath)
	config := func(listeners string) []byte {
		return fmt.Appendf(nil, `{"version":1,"realm":"fixture","state_dir":"/tmp/fixture-gateway-state","listeners":[%s]}`, listeners)
	}
	if _, err := Parse(config(`{"id":"web","protocol":"http","enabled":true,"address":"127.0.0.1:9443"},` + h3)); err != nil {
		t.Fatalf("http and http3 on one port: %v", err)
	}
	if _, err := Parse(config(`{"id":"dgram","protocol":"udp","enabled":true,"address":"127.0.0.1:9443"},` + h3)); err == nil {
		t.Fatal("two UDP listeners shared a port")
	}
	if _, err := Parse(config(`{"id":"web","protocol":"http","enabled":true,"address":"127.0.0.1:9443"},{"id":"ws","protocol":"websocket","enabled":true,"address":"127.0.0.1:9443"}`)); err == nil {
		t.Fatal("two TCP listeners shared a port")
	}
}
