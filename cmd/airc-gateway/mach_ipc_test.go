package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func machIPCCLIConfig(t *testing.T, enabled bool) (config.Config, string) {
	t.Helper()
	c, root := sharedMemoryCLIConfig(t, enabled)
	c.Listeners[0].Protocol = "mach_ipc"
	c.Listeners[0].Address = "com.example.gateway"
	c.Listeners[0].SharedMemory = nil
	c.Listeners[0].MachIPC = &config.MachIPC{Callers: []config.MachIPCCaller{{UID: machUIDPtr(501), Principal: "principal"}}}
	c.ReturnRoutes[0].Protocol = "mach_ipc"
	c.ReturnRoutes[0].Destination = "reply_port"
	return c, root
}

func machUIDPtr(uid uint32) *uint32 { return &uid }

func TestMachIPCCapabilityReflectsTargetAndHidesServiceName(t *testing.T) {
	c, root := machIPCCLIConfig(t, false)
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
	if config.MachIPCAvailable() {
		want = "disabled"
	}
	if len(report.Listeners) != 1 || report.Listeners[0].Protocol != "mach_ipc" || report.Listeners[0].Status != want {
		t.Fatalf("unexpected Mach IPC capability: %+v want=%q", report.Listeners, want)
	}
	if strings.Contains(output.String(), c.Listeners[0].Address) || strings.Contains(output.String(), c.ReturnRoutes[0].Destination) {
		t.Fatalf("capabilities disclosed Mach IPC service/reply endpoint: %s", output.String())
	}

	if config.MachIPCAvailable() {
		c, root = machIPCCLIConfig(t, true)
		path = writeSharedMemoryCLIConfig(t, c, root)
		output.Reset()
		if err := run([]string{"capabilities", "--file", path}, &output); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal("decode enabled capabilities", err, output.String())
		}
		if len(report.Listeners) != 1 || report.Listeners[0].Status != "enabled_mach_ipc_partial" {
			t.Fatalf("unexpected enabled Mach IPC capability: %+v", report.Listeners)
		}
	}
}

func TestMachIPCCLIRejectsMismatchedReplyProtocol(t *testing.T) {
	c, root := machIPCCLIConfig(t, false)
	c.ReturnRoutes[0].Protocol = "sctp"
	path := writeSharedMemoryCLIConfig(t, c, root)
	var output bytes.Buffer
	if err := run([]string{"config", "validate", "--file", path}, &output); err == nil || output.Len() != 0 {
		t.Fatalf("mismatched Mach IPC reply route accepted or reported: err=%v output=%s", err, output.String())
	}
}
