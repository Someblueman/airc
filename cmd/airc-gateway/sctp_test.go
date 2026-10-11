package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func sctpCLIConfig(t *testing.T, enabled bool) (config.Config, string) {
	t.Helper()
	c, root := sharedMemoryCLIConfig(t, enabled)
	c.Listeners[0].Protocol = "sctp"
	c.Listeners[0].Address = "127.0.0.1:0"
	c.Listeners[0].SharedMemory = nil
	c.Listeners[0].ReplyTo = ""
	return c, root
}

func sctpUnorderedCLIConfig(t *testing.T, enabled bool) (config.Config, string) {
	c, root := sctpCLIConfig(t, enabled)
	c.Listeners[0].Protocol = "sctp_unordered"
	return c, root
}

func TestSCTPCapabilitiesReportOnlyQualifiedSupport(t *testing.T) {
	if !config.SCTPAvailable() {
		t.Skip("enabled SCTP capability requires Linux ARM64")
	}
	c, root := sctpCLIConfig(t, true)
	path := writeSharedMemoryCLIConfig(t, c, root)
	var output bytes.Buffer
	if err := run([]string{"capabilities", "--file", path}, &output); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Runtime   string                   `json:"runtime"`
		Listeners []sharedMemoryCapability `json:"listeners"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal("decode capabilities", err, output.String())
	}
	if report.Runtime != "partial_not_running" || len(report.Listeners) != 1 || report.Listeners[0].Protocol != "sctp" || report.Listeners[0].Status != "enabled_sctp_partial" {
		t.Fatalf("unexpected SCTP capabilities: %+v", report)
	}
	for _, secret := range []string{root, c.StateDir, c.Upstream.Address} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("capabilities disclosed configured value %q: %s", secret, output.String())
		}
	}
}

func TestDisabledSCTPCapabilitiesShowTargetAvailability(t *testing.T) {
	c, root := sctpCLIConfig(t, false)
	path := writeSharedMemoryCLIConfig(t, c, root)
	var output bytes.Buffer
	if err := run([]string{"capabilities", "--file", path}, &output); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Listeners []sharedMemoryCapability `json:"listeners"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal("decode capabilities", err, output.String())
	}
	want := "disabled_unavailable"
	if config.SCTPAvailable() {
		want = "disabled"
	}
	if len(report.Listeners) != 1 || report.Listeners[0].Protocol != "sctp" || report.Listeners[0].Status != want || len(report.Listeners[0].Operations) != 0 {
		t.Fatalf("disabled SCTP target status=%+v want=%q", report.Listeners, want)
	}
}

func TestSCTPUnorderedCapabilitiesReportModeAndAvailability(t *testing.T) {
	c, root := sctpUnorderedCLIConfig(t, false)
	path := writeSharedMemoryCLIConfig(t, c, root)
	var output bytes.Buffer
	if err := run([]string{"capabilities", "--file", path}, &output); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Listeners []sharedMemoryCapability `json:"listeners"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal("decode capabilities", err, output.String())
	}
	want := "disabled_unavailable"
	if config.SCTPAvailable() {
		want = "disabled"
	}
	if len(report.Listeners) != 1 || report.Listeners[0].Protocol != "sctp_unordered" || report.Listeners[0].Status != want {
		t.Fatalf("unexpected unordered SCTP status: %+v want=%q", report.Listeners, want)
	}
	if config.SCTPAvailable() {
		c, root = sctpUnorderedCLIConfig(t, true)
		path = writeSharedMemoryCLIConfig(t, c, root)
		output.Reset()
		if err := run([]string{"capabilities", "--file", path}, &output); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal("decode enabled capabilities", err, output.String())
		}
		if len(report.Listeners) != 1 || report.Listeners[0].Status != "enabled_sctp_unordered_partial" {
			t.Fatalf("unexpected enabled unordered SCTP status: %+v", report.Listeners)
		}
	}
}

func TestSCTPCLIRefusesReplyDestinationAndTLS(t *testing.T) {
	cases := map[string]func(*config.Config){
		"reply destination": func(c *config.Config) { c.Listeners[0].ReplyTo = "127.0.0.1:9000" },
		"TLS option":        func(c *config.Config) { c.Listeners[0].TLSCertFile = "/cert"; c.Listeners[0].TLSKeyFile = "/key" },
		"DNS listener":      func(c *config.Config) { c.Listeners[0].Address = "localhost:0" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c, root := sctpCLIConfig(t, false)
			mutate(&c)
			path := writeSharedMemoryCLIConfig(t, c, root)
			for _, command := range [][]string{{"config", "validate", "--file", path}, {"capabilities", "--file", path}} {
				var output bytes.Buffer
				if err := run(command, &output); err == nil || output.Len() != 0 {
					t.Fatalf("invalid SCTP config accepted or reported: args=%v err=%v output=%s", command, err, output.String())
				}
			}
		})
	}
}
