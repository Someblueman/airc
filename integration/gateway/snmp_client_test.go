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
)

// A trap from the independent Net-SNMP client, forwarded byte for byte,
// posts once; the gateway's own encoder and verifier never build it.
func TestSNMPNetSNMPClientNativeDelivery(t *testing.T) {
	binary, err := exec.LookPath("snmptrap")
	if err != nil {
		t.Skip("Net-SNMP client unavailable; the route suite's independent encoder remains required")
	}
	e := newRouteEnv(t, routeCarriers[2])
	p := e.provider.(*snmpRoute)
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
	address, err := net.ResolveUDPAddr("udp", e.address)
	if err != nil {
		t.Fatal(err)
	}
	if wrote, err := relay.WriteToUDP(raw[:n], address); err != nil || wrote != n {
		t.Fatal(wrote, err)
	}
	seen := e.observed.wait(t, "SNMP Alert\nMessage: "+body)
	if seen.From != "alice" || seen.Target != routeRoom {
		t.Fatal(seen)
	}
	// The exact packet again from a new source, then a later trap as a fence.
	packet, err := snmpwire.Parse(raw[:n])
	if err != nil {
		t.Fatal(err)
	}
	peer := p.connect(t)
	peer.replay(t, routeItem{wire: [][]byte{raw[:n]}})
	p.boots, p.clock = int(packet.Boots)+1, 0
	e.posted(t, peer.deliver(t, e.text("real-client fence")))
	e.gateway.stop(t)
	if n := e.count(t, seen.Message); n != 1 {
		t.Fatalf("AIRC holds %d messages for one real-client trap", n)
	}
}
