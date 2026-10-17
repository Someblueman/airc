package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// goldenProtocols is every listener protocol name the gateway accepts, plus
// an unknown one.
var goldenProtocols = []string{
	"http", "websocket", "mcp_http", "grpc", "http3", "webtransport", "framed", "irc_websocket", "irc_connect", "mqtt_websocket",
	"unix", "unixpacket", "unixgram", "stdio", "socketpair", "inherited", "fifo", "uart",
	"system_v_queue", "posix_mqueue", "shared_memory", "mapped_file", "mach_ipc",
	"udp", "udp_lite", "ipv4_multicast", "ipv6_multicast", "ipv4_broadcast", "icmp_v6",
	"sctp", "sctp_unordered", "sctp_pr_ttl", "rawquic", "rawquic_datagrams", "dtls",
	"coap_udp", "coap_tcp", "coaps", "coaps_tcp", "coap_oscore", "webrtc", "zeromq", "nng",
	"mqtt", "mqtt_sn", "amqp", "stomp",
	"file_mailbox", "directory_mailbox", "append_log", "scp_inbox", "offline_bundle", "bpv7",
	"syslog_tls", "syslog_signed", "snmp_v3",
	"gopher",
}

// TestCapabilitiesStatusGolden pins the capabilities command's status for
// every protocol, disabled and enabled, on this platform.
func TestCapabilitiesStatusGolden(t *testing.T) {
	var b strings.Builder
	for _, protocol := range goldenProtocols {
		for _, enabled := range []bool{false, true} {
			status, ops := listenerStatus(config.Listener{ID: "l", Protocol: protocol, Enabled: enabled, Operations: []core.Operation{core.Send, core.HistoryRead}}, true)
			fmt.Fprintf(&b, "%s enabled=%t: %s %v\n", protocol, enabled, status, ops)
		}
	}
	path := filepath.Join("testdata", "capabilities_"+runtime.GOOS+"_"+runtime.GOARCH+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("no capabilities golden for this platform:", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Fatalf("%s differs (rerun with -update to inspect):\n%s", path, b.String())
	}
}

// Every Process carrier's descriptor names a command this binary runs.
func TestProcessCarrierCommands(t *testing.T) {
	ownFlags := []string{"scp-inbox", "import-bundle", "import-bpv7"}
	for _, tr := range config.Transports() {
		if tr.Kind == config.Process && processCommands[tr.Command] == nil && !slices.Contains(ownFlags, tr.Command) {
			t.Errorf("%s: no command %q", tr.Name, tr.Command)
		}
	}
}

// The report follows the registry on every platform: each listener protocol
// is listed, and reported unavailable exactly where configuration would
// refuse to enable it.
func TestCapabilitiesStatusFollowsRegistry(t *testing.T) {
	for _, tr := range config.Transports() {
		if tr.Kind == config.Bridge {
			continue
		}
		want := "disabled"
		if !config.Available(tr.Name) {
			want = "disabled_unavailable"
		}
		if status, _ := listenerStatus(config.Listener{Protocol: tr.Name}, true); status != want {
			t.Errorf("%s disabled: %s, want %s", tr.Name, status, want)
		}
		enabled := "enabled_" + tr.Name + "_partial"
		if config.NativeCarrier(tr.Name) {
			enabled = "enabled_" + tr.Name + "_native"
		}
		if status, _ := listenerStatus(config.Listener{Protocol: tr.Name, Enabled: true}, true); status != enabled {
			t.Errorf("%s enabled: %s", tr.Name, status)
		}
	}
	if status, _ := listenerStatus(config.Listener{Protocol: "irc", Enabled: true}, true); status != "disabled_unimplemented" {
		t.Errorf("a bridge name as a listener protocol: %s", status)
	}
}

// An enabled listener reports the operations its dispatcher grants: every
// implemented one and account registration when it configures none, and no
// history, search or watch without a cursor key.
func TestCapabilitiesReportServedOperations(t *testing.T) {
	all := append(dispatch.ImplementedOperations(), core.AccountRegister)
	l := config.Listener{Protocol: "udp_lite", Enabled: true}
	if _, ops := listenerStatus(l, true); !slices.Equal(ops, all) {
		t.Errorf("unconfigured listener: %v", ops)
	}
	_, ops := listenerStatus(l, false)
	if slices.Contains(ops, core.HistoryRead) || slices.Contains(ops, core.Search) || slices.Contains(ops, core.Watch) || !slices.Contains(ops, core.Send) {
		t.Errorf("without a cursor key: %v", ops)
	}
	l.Operations = []core.Operation{core.Send, core.AccountRegister}
	if _, ops := listenerStatus(l, true); !slices.Equal(ops, l.Operations) {
		t.Errorf("configured listener: %v", ops)
	}
	l.Operations = []core.Operation{core.CapabilitiesGet}
	if _, ops := listenerStatus(l, true); ops == nil || len(ops) != 0 {
		t.Errorf("no grantable operation: %#v", ops)
	}
}
