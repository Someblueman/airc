package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "airc-gateway:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "import-bpv7" {
		return importBPv7(args[1:], out)
	}
	if len(args) > 0 && args[0] == "import-bundle" {
		return importOfflineBundle(args[1:], out)
	}
	if len(args) >= 2 && args[0] == "oscore" && args[1] == "init" {
		return initializeOSCORE(args[2:], out)
	}
	command := ""
	if len(args) >= 2 && args[0] == "config" && args[1] == "validate" {
		command = "validate"
		args = args[2:]
	} else if len(args) >= 2 && args[0] == "credential" && args[1] == "init" {
		command = "credential-init"
		args = args[2:]
	} else if len(args) >= 1 && args[0] == "serve" {
		command = "serve"
		args = args[1:]
	} else if len(args) >= 1 && (args[0] == "serve-socketpair" || args[0] == "serve-inherited" || args[0] == "serve-fifo" || args[0] == "serve-uart") {
		command = args[0]
		args = args[1:]
	} else if len(args) >= 1 && args[0] == "serve-stdio" {
		command = "serve-stdio"
		args = args[1:]
	} else if len(args) >= 1 && args[0] == "mailbox-once" {
		command = "mailbox-once"
		args = args[1:]
	} else if len(args) >= 1 && args[0] == "capabilities" {
		command = "capabilities"
		args = args[1:]
	} else {
		return errors.New("usage: airc-gateway config validate --file PATH | capabilities --file PATH | credential init --file PATH | oscore init --file CONFIG --principal ID --context-file PATH | serve --file PATH | serve-stdio --file PATH | serve-socketpair --file PATH | serve-inherited --file PATH | serve-fifo --file PATH | serve-uart --file PATH | mailbox-once --file PATH | import-bundle --file CONFIG --input BUNDLE | import-bpv7 --file CONFIG --input BUNDLE")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "owner-provisioned JSON configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return errors.New("--file PATH is required; positional arguments are not accepted")
	}
	if command == "serve-socketpair" || command == "serve-inherited" {
		c, err := config.Load(*path)
		if err != nil {
			return err
		}
		source := os.NewFile(3, "gateway-parent-stream")
		if source == nil {
			return errors.New("missing inherited descriptor 3")
		}
		defer source.Close()
		open := adapter.OpenSocketPairStream
		if command == "serve-inherited" {
			open = adapter.OpenInheritedUnixStream
		}
		stream, err := open(source)
		if err != nil {
			return err
		}
		defer stream.Close()
		if command == "serve-inherited" {
			return serveInherited(c, stream)
		}
		return serveSocketPair(c, stream)
	}
	if command == "serve-stdio" {
		c, err := config.Load(*path)
		if err != nil {
			return err
		}
		stream, err := adapter.OpenPipeStream(os.Stdin, os.Stdout)
		if err != nil {
			return err
		}
		defer stream.Close()
		return serveStdio(c, stream)
	}
	if command == "credential-init" {
		if err := auth.CreateCredential(*path); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "credential created; secret stored in owner-only file")
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	if command == "serve-uart" {
		return serveUART(c)
	}
	if command == "serve-fifo" {
		return serveFIFO(c)
	}
	if command == "mailbox-once" {
		return serveFileMailbox(c)
	}
	if command == "serve" {
		return serve(c, out)
	}
	if command == "validate" {
		_, err := fmt.Fprintln(out, "configuration valid; no runtime or listeners started")
		return err
	}
	type instance struct {
		ID         string           `json:"id"`
		Protocol   string           `json:"protocol"`
		Status     string           `json:"status"`
		Operations []core.Operation `json:"operations"`
	}
	instances := make([]instance, 0, len(c.Listeners))
	for _, l := range c.Listeners {
		status := "disabled_unimplemented"
		ops := []core.Operation{}
		if l.Protocol == "postmark_email" || l.Protocol == "twilio_sms" || l.Protocol == "ipv4_multicast" || l.Protocol == "ipv6_multicast" || l.Protocol == "udp_lite" || l.Protocol == "snmp_v3" || l.Protocol == "syslog_signed" || l.Protocol == "syslog_tls" || l.Protocol == "stomp" || l.Protocol == "amqp" || l.Protocol == "mqtt_sn" || l.Protocol == "shared_memory" || l.Protocol == "mapped_file" || l.Protocol == "sctp" || l.Protocol == "sctp_unordered" || l.Protocol == "sctp_pr_ttl" || l.Protocol == "mach_ipc" || l.Protocol == "mqtt" || l.Protocol == "posix_mqueue" || l.Protocol == "http" || l.Protocol == "http3" || l.Protocol == "webtransport" || l.Protocol == "rawquic" || l.Protocol == "rawquic_datagrams" || l.Protocol == "dtls" || l.Protocol == "coaps" || l.Protocol == "coaps_tcp" || l.Protocol == "udp" || l.Protocol == "coap_udp" || l.Protocol == "coap_tcp" || l.Protocol == "coap_oscore" || l.Protocol == "unixgram" || l.Protocol == "grpc" || l.Protocol == "framed" || l.Protocol == "websocket" || l.Protocol == "mcp_http" || l.Protocol == "webrtc" || l.Protocol == "unix" || l.Protocol == "unixpacket" || l.Protocol == "stdio" || l.Protocol == "socketpair" || l.Protocol == "inherited" || l.Protocol == "file_mailbox" || l.Protocol == "directory_mailbox" || l.Protocol == "append_log" || l.Protocol == "fifo" || l.Protocol == "uart" || l.Protocol == "offline_bundle" || l.Protocol == "bpv7" {
			status = "disabled"
			if (l.Protocol == "shared_memory" && !config.SharedMemoryAvailable()) || (l.Protocol == "mapped_file" && !config.MappedFileAvailable()) || ((l.Protocol == "sctp" || l.Protocol == "sctp_unordered" || l.Protocol == "sctp_pr_ttl") && !config.SCTPAvailable()) || (l.Protocol == "mach_ipc" && !config.MachIPCAvailable()) || (l.Protocol == "posix_mqueue" && !config.POSIXQueueAvailable()) || (l.Protocol == "unixpacket" && !config.UnixPacketAvailable()) || ((l.Protocol == "unix" || l.Protocol == "unixgram" || l.Protocol == "ipv6_multicast") && !config.UnixAvailable()) || ((l.Protocol == "stdio" || l.Protocol == "socketpair" || l.Protocol == "inherited") && !config.StdioAvailable()) || ((l.Protocol == "coap_oscore" || l.Protocol == "file_mailbox" || l.Protocol == "directory_mailbox" || l.Protocol == "append_log" || l.Protocol == "fifo" || l.Protocol == "uart" || l.Protocol == "offline_bundle" || l.Protocol == "bpv7") && !config.UnixAvailable()) {
				status = "disabled_unavailable"
			}
			if l.Enabled {
				status = "enabled_" + l.Protocol + "_partial"
				ops = core.EffectiveOperations(dispatch.ImplementedOperations(), l.Operations, dispatch.ImplementedOperations(), dispatch.ImplementedOperations())
			}
		}
		instances = append(instances, instance{ID: l.ID, Protocol: l.Protocol, Status: status, Operations: ops})
	}
	return json.NewEncoder(out).Encode(struct {
		Version    int              `json:"version"`
		Runtime    string           `json:"runtime"`
		Operations []core.Operation `json:"implemented_operations"`
		Listeners  []instance       `json:"listeners"`
		Limits     config.Limits    `json:"limits"`
	}{1, "partial_not_running", dispatch.ImplementedOperations(), instances, c.Limits})
}
