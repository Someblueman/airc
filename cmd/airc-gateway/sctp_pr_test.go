package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func sctpPRTTLCLIConfig(t *testing.T, enabled bool) (config.Config, string) {
	t.Helper()
	c, root := sctpCLIConfig(t, enabled)
	c.Listeners[0].Protocol = "sctp_pr_ttl"
	return c, root
}

func TestSCTPPRTTLCapability(t *testing.T) {
	c, root := sctpPRTTLCLIConfig(t, false)
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
	if len(report.Listeners) != 1 || report.Listeners[0].Protocol != "sctp_pr_ttl" || report.Listeners[0].Status != want {
		t.Fatalf("unexpected SCTP PR-TTL capability: %+v want=%q", report.Listeners, want)
	}
	if config.SCTPAvailable() {
		c, root = sctpPRTTLCLIConfig(t, true)
		path = writeSharedMemoryCLIConfig(t, c, root)
		output.Reset()
		if err := run([]string{"capabilities", "--file", path}, &output); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal("decode enabled capabilities", err, output.String())
		}
		if len(report.Listeners) != 1 || report.Listeners[0].Status != "enabled_sctp_pr_ttl_partial" {
			t.Fatalf("unexpected enabled SCTP PR-TTL capability: %+v", report.Listeners)
		}
	}
}
