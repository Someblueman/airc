package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/service"
	"github.com/Someblueman/airc/internal/testtls"
)

func fakeExecutable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func installOptions(t *testing.T, args ...string) serviceInstallOptions {
	t.Helper()
	var install serviceInstallOptions
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	install.flags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return install
}

func argValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func TestServiceInstallBundlesGateway(t *testing.T) {
	agentEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	dir := filepath.Join(t.TempDir(), "state")
	gateway := fakeExecutable(t, "airc-gateway")
	install := installOptions(t, "--binary", fakeExecutable(t, "aircd"), "--gateway", "framed,http=8080,websocket", "--gateway-binary", gateway)
	c, err := install.config("default", dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Gateway == nil || c.Gateway.Binary != gateway || !slices.Equal(c.Gateway.Carriers, []service.Carrier{{Name: "framed", Address: "127.0.0.1:6683"}, {Name: "http", Address: "127.0.0.1:8080"}, {Name: "websocket", Address: "127.0.0.1:6681"}}) {
		t.Fatalf("%+v", c.Gateway)
	}
	if argValue(c.Args, "--gateway-token-file") != c.GatewayTokenPath() || argValue(c.Args, "--carriers") != "irc:6667,framed:6683,http:8080,websocket:6681" {
		t.Fatal(c.Args)
	}
	// Generated credentials: owner-only, 64 hex, one per role.
	seen := map[string]string{}
	for _, path := range []string{filepath.Join(dir, "admin.token"), c.GatewayTokenPath(), c.GatewayCursorKeyPath()} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(path, info, err)
		}
		token, err := admin.ReadToken(path)
		if err != nil || len(token) != 64 {
			t.Fatal(path, err)
		}
		if seen[token] != "" {
			t.Fatalf("%s repeats %s", path, seen[token])
		}
		seen[token] = path
	}
	// The generated config is validated by the gateway binary at install.
	if err := service.Install(c); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(c.GatewayConfigPath()); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	var out bytes.Buffer
	if err := printServiceStatus(&out, c, service.Status{Name: "default", Installed: true, Definition: "/d/local.airc.service", StateDir: dir, Gateway: &service.Status{Name: "gateway", Installed: true, Running: true, PID: 42, Definition: "/d/local.airc-gateway.service"}, Carriers: []string{"irc:6667", "framed:6683"}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"local.airc: running=false", "local.airc-gateway: running=true loaded=false pid=42", "Carriers (client preference order): irc:6667 framed:6683", "Gateway config: " + c.GatewayConfigPath()} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in\n%s", want, out.String())
		}
	}
	// A second install keeps the credentials but refuses the existing service.
	if _, err := install.config("default", dir); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatal(err)
	}
}

func TestServiceInstallGatewayFollowsAircdAccess(t *testing.T) {
	agentEnv(t)
	t.Setenv("HOME", t.TempDir())
	cert := testtls.New(t)
	access := filepath.Join(filepath.Dir(cert.KeyPath), "access.token")
	if err := admin.CreateToken(access); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "state")
	install := installOptions(t, "--binary", fakeExecutable(t, "aircd"), "--gateway", "http,unix", "--gateway-binary", fakeExecutable(t, "airc-gateway"), "--listen", "0.0.0.0:6697", "--tls-cert", cert.CertPath, "--tls-key", cert.KeyPath, "--access-token-file", access)
	c, err := install.config("default", dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Gateway.UpstreamServerName != "localhost" || c.Gateway.Carriers[0].Address != "0.0.0.0:6680" {
		t.Fatalf("%+v", c.Gateway)
	}
	if got := argValue(c.Args, "--carriers"); got != "irc+tls:6697,http+tls:6680,unix:"+dir+"/gateway.sock" {
		t.Fatal(got)
	}
	gateway, _ := admin.ReadToken(c.GatewayTokenPath())
	token, _ := admin.ReadToken(access)
	if gateway == token {
		t.Fatal("gateway reused the access credential")
	}
	// The gateway's strict credential reader needs an owner-only parent.
	exposed := t.TempDir()
	if err := os.Chmod(exposed, 0755); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(exposed, "access.token")
	if err := admin.CreateToken(loose); err != nil {
		t.Fatal(err)
	}
	install.access = loose
	if _, err := install.config("other", filepath.Join(t.TempDir(), "state")); err == nil || !strings.Contains(err.Error(), "owner-only (chmod 700)") {
		t.Fatal(err)
	}
}

func TestServiceInstallGatewayFlagErrors(t *testing.T) {
	agentEnv(t)
	t.Setenv("HOME", t.TempDir())
	aircd := fakeExecutable(t, "aircd")
	gateway := fakeExecutable(t, "airc-gateway")
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--gateway", "http,carrier-pigeon"}, `unknown gateway carrier "carrier-pigeon"; supported: http, websocket, mcp_http, framed, unix`},
		{[]string{"--gateway", "http=6667"}, "uses aircd's port 6667"},
		{[]string{"--gateway-binary", gateway}, "--gateway-binary requires --gateway"},
		{[]string{"--gateway", "http", "--gateway-binary", filepath.Join(t.TempDir(), "missing")}, "no such file"},
	} {
		install := installOptions(t, append([]string{"--binary", aircd, "--gateway-binary", gateway}, test.args...)...)
		dir := t.TempDir()
		if _, err := install.config("default", dir); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%v: %v", test.args, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "gateway.token")); !os.IsNotExist(err) {
			t.Errorf("%v: invalid flags wrote a credential", test.args)
		}
	}
	// Without --gateway nothing about the gateway is installed.
	c, err := installOptions(t, "--binary", aircd).config("default", t.TempDir())
	if err != nil || c.Gateway != nil || argValue(c.Args, "--carriers") != "" || argValue(c.Args, "--gateway-token-file") != "" {
		t.Fatal(c, err)
	}
}
