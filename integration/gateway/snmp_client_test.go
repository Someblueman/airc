//go:build darwin || linux

package gateway_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/testsnmp"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSNMPNetSNMPClientNativeDelivery(t *testing.T) {
	binary, err := exec.LookPath("snmptrap")
	if err != nil {
		t.Skip("Net-SNMP client unavailable; independent UDP native fixtures remain required")
	}
	f := prepareSNMPFixture(t)
	f.start(t)
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	const body = "Net-SNMP native 世界\nbody"
	work, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(work, binary, "-m", "", "-v", "3", "-l", "authNoPriv", "-a", "SHA", "-A", snmpPassword, "-u", "fixture", "-e", "0x"+snmpEngine, "-E", "0x"+snmpEngine, "udp:"+relay.LocalAddr().String(), "123", ".1.3.6.1.4.1.123.1", ".1.3.6.1.4.1.123.2", "s", body)
	root := t.TempDir()
	command.Env = append(os.Environ(), "SNMPCONFPATH="+root, "SNMP_PERSISTENT_DIR="+filepath.Join(root, "persistent"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal("Net-SNMP client", err, string(output))
	}
	if err := relay.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8193)
	n, _, err := relay.ReadFromUDP(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Forward the real client's exact bytes; no gateway encoder or verifier is
	// used to manufacture the packet observed by the native AIRC client.
	address, err := net.ResolveUDPAddr("udp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	if wrote, err := relay.WriteToUDP(raw[:n], address); err != nil || wrote != n {
		t.Fatal(wrote, err)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "SNMP Alert\nMessage: "+body })
	if event.From != "fixture-author" || event.Target != "#gateway-fixture" {
		t.Fatal(event)
	}
	// Exact packet replay through a new source, followed by a native fence.
	client := f.connect(t)
	mqttSNSend(t, client, raw[:n])
	mqttSNNoReply(t, client)
	packet, err := snmpwire.Parse(raw[:n])
	if err != nil {
		t.Fatal(err)
	}
	health := f.trap("real-client fence", 50, int(packet.Boots)+1, 1)
	mqttSNSend(t, client, testsnmp.Encode(t, health, f.key))
	next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "SNMP Alert\nMessage: real-client fence" })
	authorAwaitAccepted(t, f.receipts, f.receiptAddress, snmpRequestID(t, health))
	f.stop(t)
	if history := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(history) != 2 || history[0].ID != event.ID {
		t.Fatal("real-client replay duplicated delivery", history)
	}
}
