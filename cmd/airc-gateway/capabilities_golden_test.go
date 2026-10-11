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
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// goldenProtocols is every listener protocol name the gateway accepts, plus
// an unknown one.
var goldenProtocols = []string{
	"http", "websocket", "mcp_http", "grpc", "http3", "webtransport", "framed",
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
			status, ops := listenerStatus(config.Listener{ID: "l", Protocol: protocol, Enabled: enabled, Operations: []core.Operation{core.Send, core.HistoryRead}})
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
