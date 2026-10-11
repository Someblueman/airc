//go:build darwin || linux

package gateway_test

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

func mqttSNAwaitAccepted(t *testing.T, f *mqttSNFixture, id string) {
	t.Helper()
	awaitHTTPResult(t, f.receipts, f.receiptAddress, f.authorSpec, id, func(result core.Result) bool { return result.State == core.Accepted })
}

type mqttSNFixture struct {
	*fileMailboxFixture
	process                    *gatewayProcess
	address                    string
	receiptAddress, authorSpec string
	receipts                   *http.Client
}

func prepareMQTTSNFixture(t *testing.T) *mqttSNFixture {
	t.Helper()
	f := &mqttSNFixture{fileMailboxFixture: newFileMailboxFixture(t)}
	f.config.Listeners = []config.Listener{{ID: "sensor", Protocol: "mqtt_sn", Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Send}, MQTTSN: &config.MQTTSN{Routes: []config.MQTTSNRoute{{TopicID: 7, Target: core.Target{Kind: "room", Name: "#gateway-fixture"}}}}}}
	// This independent existing HTTPS receipt API observes durable completion;
	// MQTT-SN itself remains send-only with no success acknowledgement.
	cert := testtls.New(t)
	f.config.Listeners = append(f.config.Listeners, config.Listener{ID: "receipt-control", Protocol: "http", Enabled: true, Address: "[::1]:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: []core.Operation{core.ReceiptGet}})
	f.authorSpec = fixtureAuthor + ":" + fixtureAccount
	f.receipts = &http.Client{Transport: &http.Transport{TLSClientConfig: cert.Client.Clone()}, Timeout: 3 * time.Second}
	t.Cleanup(func() { f.receipts.CloseIdleConnections() })
	return f
}

func (f *mqttSNFixture) start(t *testing.T) {
	t.Helper()
	f.process = startGateway(t, f.binary, f.config)
	select {
	case data := <-f.process.ready:
		var ready struct {
			State     string `json:"state"`
			Listeners []struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			} `json:"listeners"`
		}
		if err := json.Unmarshal(data, &ready); err != nil || ready.State != "ready" || len(ready.Listeners) != 2 || ready.Listeners[0].ID != "sensor" || ready.Listeners[1].ID != "receipt-control" {
			t.Fatal("invalid MQTT-SN ready record", err, string(data))
		}
		f.address, f.receiptAddress = ready.Listeners[0].Address, ready.Listeners[1].Address
	case <-f.process.done:
		t.Fatal("service exited before ready", f.process.err, f.process.stderr.String())
	case <-time.After(8 * time.Second):
		t.Fatal("service did not become ready")
	}
}

func (f *mqttSNFixture) connect(t *testing.T) *net.UDPConn {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetWriteBuffer(65536); err != nil {
		t.Fatal(err)
	}
	return conn
}

// Independent MQTT-SN client encoding; no production wire package is imported.
func mqttSNPublication(topic uint16, payload string, duplicate bool) []byte {
	header := 1
	if 7+len(payload) > 255 {
		header = 3
	}
	packet := make([]byte, header+6+len(payload))
	if header == 3 {
		packet[0] = 1
		binary.BigEndian.PutUint16(packet[1:3], uint16(len(packet)))
	} else {
		packet[0] = byte(len(packet))
	}
	packet[header], packet[header+1] = 0x0c, 0x61
	if duplicate {
		packet[header+1] |= 0x80
	}
	binary.BigEndian.PutUint16(packet[header+2:header+4], topic)
	copy(packet[header+6:], payload)
	return packet
}

func mqttSNSend(t *testing.T, client *net.UDPConn, packet []byte) {
	t.Helper()
	if err := client.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := client.Write(packet); err != nil || n != len(packet) {
		t.Fatal(n, err)
	}
}

func mqttSNNoReply(t *testing.T, client *net.UDPConn) {
	t.Helper()
	if err := client.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var reply [16]byte
	if n, err := client.Read(reply[:]); err == nil {
		t.Fatal("unexpected publication acknowledgement", reply[:n])
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatal(err)
	}
}

func (f *mqttSNFixture) stop(t *testing.T) {
	t.Helper()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal(f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("MQTT-SN service did not join")
	}
}
