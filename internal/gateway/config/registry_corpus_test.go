package config

// The validation corpus records what Validate does with every listener
// protocol under a fixed set of variants: the error message, or the
// normalized listener it accepts. It was generated before the transport
// registry replaced the hand-written protocol checks, and the registry must
// reproduce it byte for byte (accepted config and error messages alike).

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/testsyslog"
)

var updateCorpus = flag.Bool("update-corpus", false, "write the corpus to AIRC_CORPUS_DIR and log its digest")

// corpusProtocols is every protocol name the gateway accepted when the corpus
// was generated, plus an unknown and an empty name.
var corpusProtocols = []string{
	"http", "websocket", "mcp_http", "grpc", "http3", "webtransport", "framed",
	"unix", "unixpacket", "unixgram", "stdio", "socketpair", "inherited", "fifo", "uart",
	"system_v_queue", "posix_mqueue", "shared_memory", "mapped_file", "mach_ipc",
	"udp", "udp_lite", "ipv4_multicast", "ipv6_multicast", "ipv4_broadcast", "icmp_v6",
	"sctp", "sctp_unordered", "sctp_pr_ttl", "rawquic", "rawquic_datagrams", "dtls",
	"coap_udp", "coap_tcp", "coaps", "coaps_tcp", "coap_oscore", "webrtc", "zeromq", "nng",
	"mqtt", "mqtt_sn", "amqp", "stomp",
	"file_mailbox", "directory_mailbox", "append_log", "scp_inbox", "offline_bundle", "bpv7",
	"syslog_tls", "syslog_signed", "snmp_v3",
	"gopher", "",
}

func corpusBase(t *testing.T, protocol string) Listener {
	t.Helper()
	send := []core.Operation{core.Send}
	room := core.Target{Kind: "room", Name: "#ops"}
	l := Listener{ID: "carrier", Protocol: protocol}
	switch protocol {
	case "unix", "unixpacket", "unixgram":
		l.Address = "/private/carrier.sock"
	case "fifo":
		l.Address, l.Operations = "/private/fifo", send
	case "uart":
		l.Address, l.Operations = "/dev/ttyfixture", send
	case "file_mailbox", "directory_mailbox", "append_log":
		l.Address, l.Operations = "/private/mailbox", send
	case "system_v_queue":
		l.Address, l.ReplyTo = "4242", "4343"
	case "posix_mqueue":
		l.Address, l.ReplyTo, l.POSIXQueue = "/airc-in", "/airc-out", &POSIXQueue{Depth: 4}
	case "shared_memory":
		l.Address, l.ReplyTo, l.SharedMemory = "/airc-shm", "/airc-shm-out", &SharedMemory{LockDirectory: "/private/locks"}
	case "mapped_file":
		l.Address, l.ReplyTo = "/private/mapped-in", "/private/mapped-out"
	case "mach_ipc":
		l.Address = "org.airc.fixture"
	case "udp", "coap_udp", "coap_tcp", "dtls", "coaps", "rawquic", "rawquic_datagrams", "http3", "webtransport":
		l.Address = "127.0.0.1:9001"
	case "udp_lite":
		l.Address, l.UDPLite = "127.0.0.1:9001", &UDPLite{ChecksumCoverage: "full"}
	case "ipv4_multicast":
		l.Address, l.IPv4Multicast = "239.255.1.2:9001", &IPv4Multicast{Interface: "lo0"}
	case "ipv6_multicast":
		l.Address, l.IPv6Multicast = "[ff12::1234]:9001", &IPv6Multicast{Interface: "lo0"}
	case "ipv4_broadcast":
		l.Address, l.IPv4Broadcast = "192.0.2.255:9001", &IPv4Broadcast{Interface: "lo0", Subnet: "192.0.2.0/24"}
	case "icmp_v6":
		l.Address, l.ICMPv6 = "[::1]:7", &ICMPv6{Interface: "lo0"}
	case "sctp", "sctp_unordered", "sctp_pr_ttl":
		l.Address = "127.0.0.1:9002"
	case "coap_oscore":
		l.Address, l.OSCORE = "127.0.0.1:9003", &OSCORE{Binary: "/opt/worker", ContextFile: "/private/context"}
	case "webrtc":
		l.TLSCertFile, l.TLSKeyFile = "/private/cert.pem", "/private/key.pem"
	case "mqtt_sn":
		l.Address, l.Operations, l.MQTTSN = "127.0.0.1:9004", send, &MQTTSN{Routes: []MQTTSNRoute{{TopicID: 7, Target: room}}}
	case "scp_inbox":
		l.Operations, l.SCPInbox = send, &SCPInbox{LoginUID: 1000, Target: room}
	case "offline_bundle":
		l.Operations = send
	case "bpv7":
		l.Operations, l.BundleProtocol = send, &BundleProtocol{Source: "dtn://producer/", Destination: "dtn://gateway/send", Target: room}
	case "syslog_tls", "syslog_signed":
		l.TLSCertFile, l.TLSKeyFile, l.Operations = "/private/cert.pem", "/private/key.pem", send
		l.Syslog = &Syslog{Routes: []SyslogRoute{{CertificateSHA256: strings.Repeat("a", 64), BoundAuthor: BoundAuthor{Nick: "syslog-host"}, Target: room}}}
		if protocol == "syslog_signed" {
			l.SignedSyslog = &SignedSyslog{Routes: []SignedSyslogRoute{{CertificateDER: base64.StdEncoding.EncodeToString(corpusOrigin(t)), Hostname: "signed.example", BoundAuthor: BoundAuthor{Nick: "signed-origin"}, Target: room}}}
		}
	case "snmp_v3":
		l.Address, l.Operations = "127.0.0.1:9005", send
		l.SNMP = &SNMP{Routes: []SNMPRoute{{EngineID: "8000000102030405", User: "fixture", Authentication: snmpwire.HMACSHA256192, AuthenticationKeyFile: "/private/snmp.key", ContextEngineID: "8000000102030405", BoundAuthor: BoundAuthor{Nick: "snmp-agent"}, Target: room, Notifications: []SNMPNotification{{OID: "1.3.6.1.4.1.123.1", Name: "Alert", Fields: []SNMPField{{OID: "1.3.6.1.4.1.123.2", Name: "Message", Type: "text", Required: true}}}}}}}
	}
	return l
}

var corpusOriginDER []byte

func corpusOrigin(t *testing.T) []byte {
	if corpusOriginDER == nil {
		now := time.Now()
		corpusOriginDER = testsyslog.New(t, now.Add(-time.Hour), now.Add(time.Hour)).DER
	}
	return corpusOriginDER
}

func corpusConfig(listeners ...Listener) Config {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "corpus", "/private/state"
	c.Upstream = &Upstream{Network: "unix", Address: "/private/airc.sock"}
	c.Listeners = listeners
	return c
}

type corpusVariant struct {
	name   string
	mutate func(c *Config, l *Listener)
}

func corpusVariants() []corpusVariant {
	yes, no := true, false
	tls := func(l *Listener) { l.TLSCertFile, l.TLSKeyFile = "/private/other-cert.pem", "/private/other-key.pem" }
	enable := func(l *Listener) { l.Enabled = true }
	v := []corpusVariant{
		{"disabled", func(*Config, *Listener) {}},
		{"enabled", func(_ *Config, l *Listener) { enable(l) }},
		{"disabled_no_address", func(_ *Config, l *Listener) { l.Address = "" }},
		{"enabled_no_address", func(_ *Config, l *Listener) { enable(l); l.Address = "" }},
		{"enabled_remote_address", func(_ *Config, l *Listener) { enable(l); l.Address = "192.0.2.10:9100" }},
		{"enabled_remote_address_tls", func(_ *Config, l *Listener) { enable(l); l.Address = "192.0.2.10:9100"; tls(l) }},
		{"enabled_ipv6_address", func(_ *Config, l *Listener) { enable(l); l.Address = "[::1]:9100" }},
		{"enabled_multicast_address", func(_ *Config, l *Listener) { enable(l); l.Address = "239.1.2.3:9100" }},
		{"enabled_hostname_address", func(_ *Config, l *Listener) { enable(l); l.Address = "localhost:9100" }},
		{"enabled_path_address", func(_ *Config, l *Listener) { enable(l); l.Address = "/private/other.sock" }},
		{"enabled_relative_path", func(_ *Config, l *Listener) { enable(l); l.Address = "relative.sock" }},
		{"enabled_long_path", func(_ *Config, l *Listener) { enable(l); l.Address = "/" + strings.Repeat("p", 120) }},
		{"enabled_tls", func(_ *Config, l *Listener) { enable(l); tls(l) }},
		{"disabled_tls", func(_ *Config, l *Listener) { tls(l) }},
		{"enabled_no_tls", func(_ *Config, l *Listener) { enable(l); l.TLSCertFile, l.TLSKeyFile = "", "" }},
		{"disabled_no_tls", func(_ *Config, l *Listener) { l.TLSCertFile, l.TLSKeyFile = "", "" }},
		{"enabled_tls_cert_only", func(_ *Config, l *Listener) { enable(l); l.TLSCertFile, l.TLSKeyFile = "/private/c.pem", "" }},
		{"enabled_origins", func(_ *Config, l *Listener) { enable(l); l.AllowedOrigins = []string{"https://example.com"} }},
		{"enabled_targets", func(_ *Config, l *Listener) { enable(l); l.Targets = []core.Target{{Kind: "room", Name: "#ops"}} }},
		{"enabled_guests_off", func(_ *Config, l *Listener) { enable(l); l.Guests = &no }},
		{"enabled_guests_on", func(_ *Config, l *Listener) { enable(l); l.Guests = &yes }},
		{"enabled_password", func(_ *Config, l *Listener) { enable(l); l.PasswordFile = "/private/password" }},
		{"enabled_limits", func(_ *Config, l *Listener) { enable(l); l.Limits = &ListenerLimits{Connections: 8} }},
		{"enabled_bound_author", func(_ *Config, l *Listener) { enable(l); l.BoundAuthor = &BoundAuthor{Nick: "robot"} }},
		{"enabled_reply_to_queue", func(_ *Config, l *Listener) { enable(l); l.ReplyTo = "/airc-reply" }},
		{"enabled_reply_to_path", func(_ *Config, l *Listener) { enable(l); l.ReplyTo = "/private/reply-dir" }},
		{"enabled_reply_to_key", func(_ *Config, l *Listener) { enable(l); l.ReplyTo = "5151" }},
		{"enabled_no_reply_to", func(_ *Config, l *Listener) { enable(l); l.ReplyTo = "" }},
		{"enabled_no_operations", func(_ *Config, l *Listener) { enable(l); l.Operations = nil }},
		{"enabled_send_only", func(_ *Config, l *Listener) { enable(l); l.Operations = []core.Operation{core.Send} }},
		{"enabled_watch_no_cursor", func(_ *Config, l *Listener) { enable(l); l.Operations = []core.Operation{core.Send, core.Watch} }},
		{"enabled_watch_cursor", func(c *Config, l *Listener) {
			enable(l)
			l.Operations = []core.Operation{core.Send, core.Watch}
			c.CursorKeyFile = "/private/cursor.key"
		}},
		{"enabled_history_one_session", func(c *Config, l *Listener) {
			enable(l)
			l.Operations = []core.Operation{core.HistoryRead}
			c.CursorKeyFile, c.Limits.SessionsPerAuthor = "/private/cursor.key", 1
		}},
		{"enabled_one_session", func(c *Config, l *Listener) { enable(l); c.Limits.Sessions, c.Limits.SessionsPerAuthor = 1, 1 }},
		{"enabled_bad_id", func(_ *Config, l *Listener) { enable(l); l.ID = "bad id" }},
		{"enabled_second_listener", func(c *Config, l *Listener) {
			enable(l)
			c.Listeners = append(c.Listeners, Listener{ID: "second", Protocol: "http", Enabled: true, Address: "127.0.0.1:9200"})
		}},
		{"enabled_second_disabled", func(c *Config, l *Listener) {
			enable(l)
			c.Listeners = append(c.Listeners, Listener{ID: "second", Protocol: "http", Address: "127.0.0.1:9200"})
		}},
		{"enabled_same_address_twin", func(c *Config, l *Listener) {
			enable(l)
			twin := *l
			twin.ID = "twin"
			c.Listeners = append(c.Listeners, twin)
		}},
		{"disabled_twin", func(c *Config, l *Listener) {
			twin := *l
			twin.ID = "twin"
			c.Listeners = append(c.Listeners, twin)
		}},
	}
	blocks := []struct {
		name string
		set  func(l *Listener)
	}{
		{"udp_lite", func(l *Listener) { l.UDPLite = &UDPLite{ChecksumCoverage: "full"} }},
		{"ipv4_broadcast", func(l *Listener) { l.IPv4Broadcast = &IPv4Broadcast{Interface: "lo0", Subnet: "192.0.2.0/24"} }},
		{"ipv6_multicast", func(l *Listener) { l.IPv6Multicast = &IPv6Multicast{Interface: "lo0"} }},
		{"icmp_v6", func(l *Listener) { l.ICMPv6 = &ICMPv6{Interface: "lo0"} }},
		{"ipv4_multicast", func(l *Listener) { l.IPv4Multicast = &IPv4Multicast{Interface: "lo0"} }},
		{"snmp", func(l *Listener) { l.SNMP = &SNMP{} }},
		{"signed_syslog", func(l *Listener) { l.SignedSyslog = &SignedSyslog{} }},
		{"syslog", func(l *Listener) { l.Syslog = &Syslog{} }},
		{"stomp", func(l *Listener) { l.STOMP = &STOMP{} }},
		{"amqp", func(l *Listener) { l.AMQP = &AMQP{} }},
		{"mqtt_sn", func(l *Listener) { l.MQTTSN = &MQTTSN{} }},
		{"mqtt", func(l *Listener) { l.MQTT = &MQTT{} }},
		{"bundle_protocol", func(l *Listener) { l.BundleProtocol = &BundleProtocol{} }},
		{"posix_queue", func(l *Listener) { l.POSIXQueue = &POSIXQueue{Depth: 4} }},
		{"shared_memory", func(l *Listener) { l.SharedMemory = &SharedMemory{LockDirectory: "/private/locks2"} }},
		{"mach_ipc", func(l *Listener) { l.MachIPC = &MachIPC{} }},
		{"webrtc", func(l *Listener) { l.WebRTC = &WebRTC{} }},
		{"oscore", func(l *Listener) { l.OSCORE = &OSCORE{Binary: "/opt/worker", ContextFile: "/private/context"} }},
		{"scp_inbox", func(l *Listener) {
			l.SCPInbox = &SCPInbox{LoginUID: 1000, Target: core.Target{Kind: "room", Name: "#ops"}}
		}},
	}
	for _, b := range blocks {
		v = append(v, corpusVariant{"enabled_block_" + b.name, func(_ *Config, l *Listener) { enable(l); b.set(l) }})
		v = append(v, corpusVariant{"disabled_block_" + b.name, func(_ *Config, l *Listener) { b.set(l) }})
	}
	return v
}

// corpusOutcome is one variant's result: the error, or the accepted
// listeners after normalization.
func corpusOutcome(c Config) string {
	if err := c.Validate(); err != nil {
		return "error: " + err.Error()
	}
	data, err := json.Marshal(c.Listeners)
	if err != nil {
		return "marshal: " + err.Error()
	}
	return "ok: " + string(data)
}

func TestValidationCorpus(t *testing.T) {
	var b strings.Builder
	for _, protocol := range corpusProtocols {
		for _, v := range corpusVariants() {
			l := corpusBase(t, protocol)
			c := corpusConfig()
			v.mutate(&c, &l)
			c.Listeners = append([]Listener{l}, c.Listeners...)
			fmt.Fprintf(&b, "%s/%s: %s\n", protocol, v.name, corpusOutcome(c))
		}
	}
	got := strings.ReplaceAll(b.String(), base64.StdEncoding.EncodeToString(corpusOrigin(t)), "<origin>")
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(got)))
	if *updateCorpus {
		path := filepath.Join(t.TempDir(), "corpus")
		if dir := os.Getenv("AIRC_CORPUS_DIR"); dir != "" {
			path = filepath.Join(dir, "validation_corpus_"+runtime.GOOS+"_"+runtime.GOARCH)
		}
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("corpus %s sha256 %s", path, sum)
	}
	want, ok := corpusSums[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		t.Skip("no corpus recorded for this platform")
	}
	if sum != want {
		t.Fatalf("validation corpus changed: sha256 %s, want %s; rerun with -update-corpus and AIRC_CORPUS_DIR to diff", sum, want)
	}
}

// corpusSums are the corpus digests recorded before the registry.
var corpusSums = map[string]string{
	"linux/amd64": "758057302c3f0e3666added65b2152cbfec03fabcc52ed892253cda39e6faed1",
}

// TestCorpusBasesAreValid keeps the corpus meaningful: every known
// protocol's base listener is accepted while disabled.
func TestCorpusBasesAreValid(t *testing.T) {
	for _, protocol := range corpusProtocols {
		if protocol == "" {
			continue
		}
		c := corpusConfig(corpusBase(t, protocol))
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", protocol, err)
		}
	}
}
