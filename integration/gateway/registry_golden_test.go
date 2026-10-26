//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/service"
	"github.com/Someblueman/airc/internal/testtls"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// TestEveryCarrierGolden runs one gateway with every carrier this host can
// serve (the cross-carrier suite's carriers whose setup does not skip) and
// pins what clients discover: the carrier list aircd advertises in 005
// CARRIERS and STATUS for an install of every bundled carrier, and
// capabilities.get over each carrier (GET /v1/capabilities on HTTP). The
// ready record must list the listeners in configuration order. Entries for
// carriers this host cannot run are kept in the golden file and skipped.
func TestEveryCarrierGolden(t *testing.T) {
	root := fixtureRoot(t)
	discovery := installDiscovery(t)
	gatewayToken := filepath.Join(root, "gateway.token")
	if err := auth.CreateCredential(gatewayToken); err != nil {
		t.Fatal(err)
	}
	socket := startNative(t, root, false, "none", "--gateway-token-file", gatewayToken, "--carriers", strings.Join(discovery, ","))
	provision := dial(t, socket, "alice", aliceToken, true, true)
	_ = provision.Close()

	got := map[string]string{}
	status := nativeStatusClient(t, socket)
	got["005 CARRIERS"] = status.Features()["CARRIERS"]
	got["STATUS carriers"] = strings.Join(nativeStatus(t, status).Carriers, ",")

	// Every suite carrier whose setup this host supports, each once.
	cert := testtls.NewP256(t)
	var carriers []suiteCarrier
	var listeners []config.Listener
	for _, c := range suiteCarriers {
		if c.configure != nil && !hostRuns(t, c) {
			t.Logf("%s: not available on this host", c.name)
			continue
		}
		l := c.listener
		l.ID, l.Limits = c.name, fixtureListenerLimits()
		if c.secure {
			l.TLSCertFile, l.TLSKeyFile = cert.CertPath, cert.KeyPath
		}
		if c.configure != nil {
			c.configure(t, &l)
		}
		carriers = append(carriers, c)
		listeners = append(listeners, l)
	}
	cfg := authorConfig(t, socket, gatewayToken, listeners...)
	cfg.Carriers = discovery
	cfg.Limits.Sessions = 128

	distinctAddresses(t, &cfg)
	started := time.Now()
	ready := startReadyGateway(t, cfg)
	t.Logf("gateway startup with %d listeners: %v", len(ready.Listeners), time.Since(started))
	var want, have []string
	for _, l := range cfg.Listeners {
		if l.Enabled {
			want = append(want, l.ID)
		}
	}
	addresses := map[string]string{}
	for _, l := range ready.Listeners {
		have = append(have, l.ID)
		addresses[l.ID] = l.Address
	}
	if !slices.Equal(have, want) {
		t.Fatalf("ready listeners %v, want configuration order %v", have, want)
	}
	for _, c := range carriers {
		d := suiteDial{address: addresses[c.name]}
		if c.secure {
			d.tls = cert.Client.Clone()
		}
		client, err := c.dial(t, d)
		if err != nil {
			t.Fatalf("%s: connect: %v", c.name, err)
		}
		r := client.call(t, mustBody(t, plainRequest(map[string]any{"request_id": "caps-" + c.name, "operation": core.CapabilitiesGet}, "caps-"+c.name, "")))
		client.close()
		if r.result != nil && r.result.Capabilities != nil {
			// The capabilities command reports what the carrier grants.
			i := slices.IndexFunc(cfg.Listeners, func(l config.Listener) bool { return l.ID == c.name })
			if want := dispatch.ListenerOperations(cfg.Listeners[i], true); !slices.Equal(r.result.Capabilities.Operations, want) {
				t.Errorf("%s: capabilities.get operations %v, the capabilities command reports %v", c.name, r.result.Capabilities.Operations, want)
			}
			data, err := json.Marshal(r.result.Capabilities)
			if err != nil {
				t.Fatal(err)
			}
			got["capabilities "+c.name] = string(data)
		} else {
			// MQTT carries only mutations: the request has no answer.
			got["capabilities "+c.name] = r.String()
		}
	}
	compareGolden(t, filepath.Join("testdata", "carriers.golden"), got)
}

// hostRuns reports whether a carrier's setup runs on this host: it skips
// when the host lacks the feature. The trial's resources are released.
func hostRuns(t *testing.T, c suiteCarrier) bool {
	ran := false
	t.Run("probe_"+c.name, func(t *testing.T) {
		l := c.listener
		c.configure(t, &l)
		ran = true
	})
	return ran
}

// installDiscovery is the carrier list airc service install --gateway
// advertises for every bundled carrier, aircd on TLS.
func installDiscovery(t *testing.T) []string {
	t.Helper()
	carriers, err := service.ParseCarriers(strings.Join(service.SupportedCarriers(), ","), "127.0.0.1", "/var/lib/airc")
	if err != nil {
		t.Fatal(err)
	}
	c := service.Config{Name: "default", StateDir: "/var/lib/airc", Args: []string{"--listen", "127.0.0.1:6697", "--tls-cert", "/etc/airc/cert.pem"}, Gateway: &service.Gateway{Carriers: carriers}}
	list, err := c.Discovery()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range list {
		out = append(out, entry.String())
	}
	return out
}

type readyRecord struct {
	Listeners []struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	} `json:"listeners"`
}

// startReadyGateway starts the gateway like startAuthorGateway and returns
// its whole ready record.
func startReadyGateway(t *testing.T, c config.Config) readyRecord {
	t.Helper()
	g := startAuthorGateway(t, c)
	var ready readyRecord
	if err := json.Unmarshal(g.ready, &ready); err != nil {
		t.Fatal(err)
	}
	return ready
}

// compareGolden checks got against a golden file of "key: value" lines.
// Keys the host did not produce are skipped; a new key fails.
func compareGolden(t *testing.T, path string, got map[string]string) {
	t.Helper()
	if *updateGolden {
		var keys []string
		for key := range got {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		var b strings.Builder
		for _, key := range keys {
			fmt.Fprintf(&b, "%s: %s\n", key, got[key])
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]string{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		key, value, _ := strings.Cut(scanner.Text(), ": ")
		want[key] = value
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for key, value := range got {
		expected, ok := want[key]
		if !ok {
			t.Errorf("%s: not in %s (rerun with -update): %s", key, path, value)
		} else if value != expected {
			t.Errorf("%s:\n got: %s\nwant: %s", key, value, expected)
		}
	}
	for key := range want {
		if _, ok := got[key]; !ok {
			t.Logf("%s: not run on this host", key)
		}
	}
}

// distinctAddresses gives a fresh port to an enabled listener whose
// reserved port another network's reservation also drew: the gateway
// refuses two enabled listeners on one address, whatever their networks.
func distinctAddresses(t *testing.T, c *config.Config) {
	t.Helper()
	seen := map[string]bool{}
	for i := range c.Listeners {
		l := &c.Listeners[i]
		for l.Enabled && seen[l.Address] {
			network := "tcp"
			if tr, _ := config.Lookup(l.Protocol); tr.Network == "udp" || tr.Kind == config.Packet {
				network = "udp"
			}
			var probe io.Closer
			var address string
			if network == "tcp" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				probe, address = listener, listener.Addr().String()
			} else {
				conn, err := net.ListenPacket("udp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				probe, address = conn, conn.LocalAddr().String()
			}
			_ = probe.Close()
			l.Address = address
		}
		seen[l.Address] = true
	}
}
