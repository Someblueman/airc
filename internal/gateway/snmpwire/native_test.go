package snmpwire

import (
	"bytes"
	"context"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testsnmp"
)

func TestNetSNMPUSMTrapExactAuthentication(t *testing.T) {
	binary, err := exec.LookPath("snmptrap")
	if err != nil {
		t.Skip("Net-SNMP client unavailable")
	}
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	engine, _ := hex.DecodeString("8000000102030405")
	const password = "test-only-fixture-password"
	const body = "Net-SNMP exact 世界\nbody"
	work, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(work, binary, "-m", "", "-v", "3", "-l", "authNoPriv", "-a", "SHA", "-A", password, "-u", "fixture", "-e", "0x8000000102030405", "-E", "0x8000000102030405", "udp:"+conn.LocalAddr().String(), "123", ".1.3.6.1.4.1.123.1", ".1.3.6.1.4.1.123.2", "s", body)
	root := t.TempDir()
	command.Env = append(os.Environ(), "SNMPCONFPATH="+root, "SNMP_PERSISTENT_DIR="+filepath.Join(root, "persistent"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal("Net-SNMP client failed", err, string(output))
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buffer := make([]byte, MaxPacket+1)
	n, _, err := conn.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	key := testsnmp.LocalizedKey(password, engine, false)
	verifier, err := NewVerifier(HMACSHA196, key)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := verifier.Verify(buffer[:n])
	t.Logf("Net-SNMP authoritative engine boots=%d time=%d", packet.Boots, packet.Time)
	if err != nil || packet.User != "fixture" || !bytes.Equal(packet.EngineID, engine) || !bytes.Equal(packet.ContextEngineID, engine) || packet.Uptime != 123 || packet.Notification != "1.3.6.1.4.1.123.1" || len(packet.Variables) != 1 || string(packet.Variables[0].Bytes) != body {
		t.Fatal("independent Net-SNMP trap", packet, err)
	}
}
