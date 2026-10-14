package service

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// TestGatewayCarriersGolden pins what airc service install --gateway
// produces for every carrier it supports: the carrier list aircd advertises
// in 005 CARRIERS and STATUS (and the gateway in capabilities.get), and the
// generated gateway configuration, without TLS, with TLS and an access
// token, and on a Unix socket.
func TestGatewayCarriersGolden(t *testing.T) {
	const state = "/var/lib/airc"
	var b strings.Builder
	fmt.Fprintf(&b, "supported: %s\n", strings.Join(SupportedCarriers(), ","))
	for _, v := range []struct {
		name string
		args []string
	}{
		{"plain", []string{"--listen", "127.0.0.1:6667"}},
		{"tls_access", []string{"--listen", "0.0.0.0:6697", "--tls-cert", "/etc/airc/cert.pem", "--tls-key", "/etc/airc/key.pem", "--access-token-file", "/etc/airc/access.token"}},
		{"unix", []string{"--listen", "127.0.0.1:6667", "--unix", "/run/airc/irc.sock"}},
	} {
		names := SupportedCarriers()
		if !slices.Contains(v.args, "--tls-cert") || slices.Contains(v.args, "--unix") {
			names = slices.DeleteFunc(names, carrierNeedsTLS) // refused without aircd's certificate
		}
		carriers, err := ParseCarriers(strings.Join(names, ","), "127.0.0.1", state)
		if err != nil {
			t.Fatal(err)
		}
		c := Config{Name: "default", Binary: "/opt/airc/aircd", StateDir: state, Args: v.args, Gateway: &Gateway{Binary: "/opt/airc/airc-gateway", Carriers: carriers, UpstreamServerName: "airc.example"}}
		discovery, err := c.Discovery()
		if err != nil {
			t.Fatal(v.name, err)
		}
		var list []string
		for _, entry := range discovery {
			list = append(list, entry.String())
		}
		data, err := c.GatewayConfig()
		if err != nil {
			t.Fatal(v.name, err)
		}
		fmt.Fprintf(&b, "\n== %s\nCARRIERS=%s\n%s", v.name, strings.Join(list, ","), data)
	}
	checkGolden(t, "gateway_carriers.golden", b.String())
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs (rerun with -update to inspect):\n%s", path, got)
	}
}
