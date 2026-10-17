package service

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/admin"
	gwconfig "github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testtls"
)

// bundled is a service with a gateway whose credentials exist in an
// owner-only state directory, as airc service install leaves them.
func bundled(t *testing.T, args []string, carriers string) Config {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c := Config{Name: "default", Binary: "/opt/airc/aircd", StateDir: dir, Args: args}
	list, err := ParseCarriers(carriers, "127.0.0.1", dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Gateway = &Gateway{Binary: "/opt/airc/airc-gateway", Carriers: list}
	for _, path := range []string{c.GatewayTokenPath(), c.GatewayCursorKeyPath()} {
		if err := admin.CreateToken(path); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestParseCarriersDefaultsOverridesAndErrors(t *testing.T) {
	dir := "/home/a/.local/state/airc"
	got, err := ParseCarriers("http,websocket=8081,framed=0.0.0.0:7000,unix", "127.0.0.1", dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Carrier{{"http", "127.0.0.1:6680"}, {"websocket", "127.0.0.1:8081"}, {"framed", "0.0.0.0:7000"}, {"unix", dir + "/gateway.sock"}}
	if !slices.Equal(got, want) {
		t.Fatalf("%+v", got)
	}
	if got, err := ParseCarriers("mcp_http,unix=/run/airc.sock", "::1", dir); err != nil || got[0].Address != "[::1]:6682" || got[1].Address != "/run/airc.sock" {
		t.Fatal(got, err)
	}
	for spec, message := range map[string]string{
		"gopher":            `unknown gateway carrier "gopher"; supported: http, websocket, mcp_http, framed, unix, irc_websocket, irc_connect, grpc, mqtt, amqp, stomp, coap_udp, coap_tcp, zeromq, nng, udp, http3`,
		"http,http=81":      "listed twice",
		"http=0":            "port 1-65535",
		"http=70000":        "port 1-65535",
		"http=localhost:8":  "numeric IP",
		"unix=relative":     "absolute socket path",
		"http=81,framed=81": "share address",
		"":                  "unknown gateway carrier",
	} {
		if _, err := ParseCarriers(spec, "127.0.0.1", dir); err == nil || !strings.Contains(err.Error(), message) {
			t.Errorf("%q: %v", spec, err)
		}
	}
}

// Every listed carrier, brokers included, parses on its own with its default
// port and all of them together in one install.
func TestEachListedCarrierParses(t *testing.T) {
	dir := "/home/a/.local/state/airc"
	for _, spec := range carrierSpecs {
		got, err := ParseCarriers(spec.name, "127.0.0.1", dir)
		if err != nil || len(got) != 1 || got[0].Name != spec.name {
			t.Fatalf("%s: %+v %v", spec.name, got, err)
		}
		if spec.port != 0 && got[0].Address != "127.0.0.1:"+strconv.Itoa(spec.port) {
			t.Errorf("%s: default address %s", spec.name, got[0].Address)
		}
	}
	if got := SupportedCarriers(); len(got) != len(carrierSpecs) {
		t.Fatalf("supported %v", got)
	}
	for _, name := range []string{"mqtt", "amqp", "stomp", "grpc", "coap_udp", "coap_tcp", "zeromq", "nng", "udp"} {
		if !slices.Contains(SupportedCarriers(), name) {
			t.Errorf("%s is not offered", name)
		}
	}
	plain := slices.DeleteFunc(SupportedCarriers(), carrierNeedsTLS)
	c := bundled(t, []string{"--listen", "127.0.0.1:6667"}, strings.Join(plain, ","))
	data, err := c.GatewayConfig()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := gwconfig.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range loaded.Listeners {
		switch l.Protocol {
		case "mqtt":
			if len(l.MQTT.Routes) != 1 || l.MQTT.Routes[0].Topic != gwconfig.DefaultMQTTTopic {
				t.Errorf("mqtt routes %+v", l.MQTT.Routes)
			}
		case "amqp":
			if len(l.AMQP.Routes) != 1 || l.AMQP.Routes[0].RequestAddress != "airc.requests" || l.AMQP.Routes[0].ResponseAddress != "airc.responses" {
				t.Errorf("amqp routes %+v", l.AMQP.Routes)
			}
		case "stomp":
			if len(l.STOMP.Routes) != 1 || l.STOMP.Routes[0].RequestDestination != "airc.requests" || l.STOMP.Routes[0].ResponseDestination != "airc.responses" {
				t.Errorf("stomp routes %+v", l.STOMP.Routes)
			}
		}
	}
}

func TestEverySupportedCarrierGeneratesAValidConfig(t *testing.T) {
	cert := testtls.New(t)
	access := filepath.Join(filepath.Dir(cert.KeyPath), "access.token")
	if err := admin.CreateToken(access); err != nil {
		t.Fatal(err)
	}
	for _, name := range SupportedCarriers() {
		args := []string{"--listen", "127.0.0.1:6667"}
		if carrierNeedsTLS(name) {
			args = append(args, "--tls-cert", cert.CertPath, "--tls-key", cert.KeyPath, "--access-token-file", access)
		}
		c := bundled(t, args, name)
		c.Gateway.UpstreamServerName = "localhost"
		data, err := c.GatewayConfig()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := gwconfig.Parse(data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestGeneratedGatewayConfigLoadsAndFollowsAircd(t *testing.T) {
	c := bundled(t, []string{"--listen", "0.0.0.0:6667"}, "framed,http=0.0.0.0:8080,websocket,unix")
	c.Args = append(c.Args, "--unix", "/run/user/1/airc.sock")
	data, err := c.GatewayConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.GatewayConfigPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(c.GatewayStateDir(), 0700); err != nil {
		t.Fatal(err)
	}
	loaded, err := gwconfig.Load(c.GatewayConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	// One upstream on aircd's socket with its gateway credential; nothing
	// else to provision.
	if u := loaded.Upstream; u == nil || u.Network != "unix" || u.Address != "/run/user/1/airc.sock" || u.GatewayTokenFile != c.GatewayTokenPath() || u.AccessTokenFile != "" {
		t.Fatalf("upstream %+v", loaded.Upstream)
	}
	for _, field := range []string{"principals", "bindings", "recipient_keys", "response_signing_key", "return_routes"} {
		if strings.Contains(string(data), field) {
			t.Fatal("generated removed identity field", field, string(data))
		}
	}
	if loaded.StateDir != c.GatewayStateDir() || loaded.CursorKeyFile != c.GatewayCursorKeyPath() || loaded.Realm != "default" {
		t.Fatal(string(data))
	}
	var ids []string
	for _, l := range loaded.Listeners {
		ids = append(ids, l.ID+"@"+l.Address)
		if !l.Enabled || len(l.Operations) != 0 || !l.GuestsAllowed() || l.TLSCertFile != "" || l.PasswordFile != "" {
			t.Fatalf("listener %+v", l)
		}
	}
	if strings.Join(ids, " ") != "framed@127.0.0.1:6683 http@0.0.0.0:8080 websocket@127.0.0.1:6681 unix@"+c.StateDir+"/gateway.sock" {
		t.Fatal(ids)
	}
	if strings.Join(loaded.Carriers, ",") != "irc:/run/user/1/airc.sock,framed:6683,http:8080,websocket:6681,unix:"+c.StateDir+"/gateway.sock" {
		t.Fatal(loaded.Carriers)
	}
}

func TestGatewayKeepsAircdAccessPolicy(t *testing.T) {
	cert := testtls.New(t)
	access := filepath.Join(filepath.Dir(cert.KeyPath), "access.token")
	if err := admin.CreateToken(access); err != nil {
		t.Fatal(err)
	}
	c := bundled(t, []string{"--listen", "0.0.0.0:6697", "--tls-cert", cert.CertPath, "--tls-key", cert.KeyPath, "--access-token-file", access}, "http,unix,websocket")
	c.Gateway.UpstreamServerName = "localhost"
	data, err := c.GatewayConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.GatewayConfigPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	// Listeners share aircd's certificate, key and access token, which is
	// also the upstream's credential: one role each, so no aliasing.
	loaded, err := gwconfig.Load(c.GatewayConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if u := loaded.Upstream; u.Network != "tcp" || u.Address != "127.0.0.1:6697" || u.TLSServerName != "localhost" || u.TLSCAFile != cert.CertPath || u.AccessTokenFile != access {
		t.Fatalf("upstream %+v", u)
	}
	http, unix := loaded.Listeners[0], loaded.Listeners[1]
	if http.TLSCertFile != cert.CertPath || http.TLSKeyFile != cert.KeyPath || http.PasswordFile != access || unix.TLSCertFile != "" || unix.PasswordFile != access {
		t.Fatalf("listeners %+v %+v", http, unix)
	}
	if strings.Join(loaded.Carriers, ",") != "irc+tls:6697,http+tls:6680,unix:"+c.StateDir+"/gateway.sock,websocket+tls:6681" {
		t.Fatal(loaded.Carriers)
	}
}

func TestGatewayJobsRenderAsADependentPair(t *testing.T) {
	c := bundled(t, []string{"--listen", "127.0.0.1:6667"}, "http")
	c.Name = "work"
	if c.Label() != "local.airc.work" || c.GatewayLabel() != "local.airc-gateway.work" {
		t.Fatal(c.Label(), c.GatewayLabel())
	}
	// A service named "gateway" cannot collide with the default gateway job.
	if (Config{Name: "gateway"}).Label() == (Config{Name: "default"}).GatewayLabel() {
		t.Fatal("labels collide")
	}
	unit, err := renderJob(c, c.gatewayJob(), "linux")
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	for _, want := range []string{
		"Description=AIRC gateway carriers\nWants=local.airc.work.service\nAfter=network.target local.airc.work.service\n",
		`ExecStart="/opt/airc/airc-gateway" "serve" "--file" "` + c.StateDir + `/gateway.json"`,
		"Restart=on-failure\n", "TimeoutStopSec=20\n", "UMask=0077\n", "WantedBy=default.target\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	daemon, err := render(c, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(daemon), "[Unit]\nDescription=AIRC agent chat service\nAfter=network.target\n\n[Service]\nType=exec\nExecStart=\"/opt/airc/aircd\" \"--listen\"") || strings.Contains(string(daemon), "Wants=") {
		t.Fatal(string(daemon))
	}
	plist, err := renderJob(c, c.gatewayJob(), "darwin")
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(strings.NewReader(string(plist)))
	var values []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if element, ok := token.(xml.StartElement); ok && element.Name.Local == "string" {
			var value string
			if err := decoder.DecodeElement(&value, &element); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
	}
	if !slices.Equal(values, []string{"local.airc-gateway.work", "/opt/airc/airc-gateway", "serve", "--file", c.GatewayConfigPath(), c.StateDir, "Background", "/dev/null", c.GatewayLogPath()}) {
		t.Fatal(values)
	}
	if jobs := c.jobs(); len(jobs) != 2 || jobs[0].label != c.Label() || jobs[1].label != c.GatewayLabel() {
		t.Fatal("aircd must start first", jobs)
	}
}

func TestInstallWritesTheGatewayPairOrNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	c := bundled(t, []string{"--listen", "127.0.0.1:6667"}, "http,websocket")
	discovery, err := c.Discovery()
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery) != 3 || discovery[0].String() != "irc:6667" || discovery[2].String() != "websocket:6681" {
		t.Fatal(discovery)
	}
	refused := errors.New("refused")
	original := validateGateway
	t.Cleanup(func() { validateGateway = original })
	validateGateway = func(string, string) error { return refused }
	if err := Install(c); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	for _, path := range []string{c.GatewayConfigPath(), filepath.Join(c.StateDir, "service.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("left", path)
		}
	}
	var validated string
	validateGateway = func(binary, path string) error {
		_, err := gwconfig.Load(path)
		validated = binary
		return err
	}
	if err := Install(c); err != nil {
		t.Fatal(err)
	}
	if validated != c.Gateway.Binary {
		t.Fatal("gateway binary did not validate its configuration")
	}
	daemonPath, _ := c.Manifest()
	gatewayPath, _ := c.GatewayManifest()
	for _, path := range []string{daemonPath, gatewayPath, c.GatewayConfigPath(), filepath.Join(c.StateDir, "service.json")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(path, info, err)
		}
	}
	if info, err := os.Stat(c.GatewayStateDir()); err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("gateway state directory", err)
	}
	loaded, err := Load(c.StateDir, c.Name)
	if err != nil || loaded.Gateway == nil || !slices.Equal(loaded.Gateway.Carriers, c.Gateway.Carriers) {
		t.Fatal(loaded, err)
	}
	for _, j := range loaded.jobs() {
		path, _ := loaded.manifest(runtime.GOOS, j.label)
		if err := ownedDefinition(loaded, j, path); err != nil {
			t.Fatal(j.label, err)
		}
	}
	if err := Install(c); err == nil {
		t.Fatal("overwrote an installed service")
	}
	var raw map[string]any
	data, _ := os.ReadFile(filepath.Join(c.StateDir, "service.json"))
	if err := json.Unmarshal(data, &raw); err != nil || raw["gateway"] == nil {
		t.Fatal(string(data))
	}
}

func TestServiceWithoutGatewayIsUnchanged(t *testing.T) {
	c := Config{Name: "default", Binary: "/opt/airc/aircd", StateDir: "/tmp/airc", Args: []string{"--listen", "127.0.0.1:6667"}}
	if jobs := c.jobs(); len(jobs) != 1 {
		t.Fatal(jobs)
	}
	if d, err := c.Discovery(); err != nil || d != nil {
		t.Fatal(d, err)
	}
	data, _ := json.Marshal(c)
	if strings.Contains(string(data), "gateway") {
		t.Fatal(string(data))
	}
}

// HTTP/3 runs only with aircd's certificate, and on UDP it may share http's
// port number.
func TestHTTP3UsesAircdsCertificateAndSharesHTTPsPort(t *testing.T) {
	plain := bundled(t, []string{"--listen", "127.0.0.1:6667"}, "http,http3")
	if _, err := plain.Discovery(); err == nil || !strings.Contains(err.Error(), "needs TLS") {
		t.Fatalf("http3 without TLS: %v", err)
	}
	if _, err := ParseCarriers("http,framed=6680", "127.0.0.1", t.TempDir()); err == nil {
		t.Fatal("two TCP carriers shared a port")
	}
	for name, want := range map[string]string{"http3": "udp", "udp": "udp", "coap_udp": "udp", "coap_tcp": "tcp", "http": "tcp"} {
		if got := CarrierNetwork(name); got != want {
			t.Errorf("%s listens on %s, not %s", name, got, want)
		}
	}
	cert := testtls.New(t)
	access := filepath.Join(filepath.Dir(cert.KeyPath), "access.token")
	if err := admin.CreateToken(access); err != nil {
		t.Fatal(err)
	}
	c := bundled(t, []string{"--listen", "127.0.0.1:6697", "--tls-cert", cert.CertPath, "--tls-key", cert.KeyPath, "--access-token-file", access}, "http,http3")
	c.Gateway.UpstreamServerName = "localhost"
	discovery, err := c.Discovery()
	if err != nil {
		t.Fatal(err)
	}
	if got := discovery[2].String(); got != "http3+tls:6680" {
		t.Fatalf("advertised %s", got)
	}
	data, err := c.GatewayConfig()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := gwconfig.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range loaded.Listeners {
		if l.Protocol == "http3" && (l.TLSCertFile != cert.CertPath || l.Address != "127.0.0.1:6680") {
			t.Fatalf("listener %+v", l)
		}
	}
}
