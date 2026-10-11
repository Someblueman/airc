//go:build darwin || linux

package adapter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/mqttsnwire"
)

// Encodes the independent client datagram, without using the production codec.
func mqttSNDatagram(topic uint16, payload string) []byte {
	packet := make([]byte, 9+len(payload))
	packet[0], packet[3], packet[4] = 1, 0x0c, 0x61
	binary.BigEndian.PutUint16(packet[1:3], uint16(len(packet)))
	binary.BigEndian.PutUint16(packet[5:7], topic)
	copy(packet[9:], payload)
	return packet
}

func TestMQTTSNUDPAdmissionErrorAndCancellation(t *testing.T) {
	f := newMQTTSNFixture(t)
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	client, err := net.Dial("udp", server.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// Darwin's default UDP send buffer is smaller than the profile bound.
	// Increase only the isolated client's buffer to exercise actual oversize input.
	if err := client.(*net.UDPConn).SetWriteBuffer(65536); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	options := config.MQTTSN{Routes: []config.MQTTSNRoute{mqttSNRoute()}}
	requests := make(chan struct{}, 1)
	go func() { done <- ServeMQTTSN(ctx, server, f.d, requests, options) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("UDP owner did not join cleanup")
			}
		}
	})
	payload := f.body(t, mqttSNRequest())
	if _, err := client.Write(mqttSNDatagram(7, payload)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := f.lookup(t, "mqtt-sn-request")
		if err == nil && job.Result.Durable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("UDP did not admit", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Durable intake is externally visible, yet QoS -1 sends no success PUBACK.
	if err := client.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var reply [16]byte
	if _, err := client.Read(reply[:]); err == nil {
		t.Fatal("unexpected success acknowledgement")
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		mqttSNDatagram(8, "invalid"),
		mqttSNDatagram(7, "invalid"),
		mqttSNDatagram(7, string(bytes.Repeat([]byte{'x'}, mqttsnwire.MaxPayloadBytes+1))),
	} {
		if _, err := client.Write(bad); err != nil {
			t.Fatal(err)
		}
		if err := client.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Read(reply[:]); err == nil {
			t.Fatal("malformed or oversized packet triggered response")
		} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			t.Fatal(err)
		}
	}
	if _, err := client.Write(mqttSNDatagram(8, payload)); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := client.Read(reply[:])
	if err != nil || !bytes.Equal(reply[:n], []byte{7, 0x0d, 0, 8, 0, 0, 2}) {
		t.Fatal("invalid-topic response", reply[:n], err)
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("UDP receive did not join cancellation")
	}
	if _, _, err := server.ReadFrom(reply[:]); !errors.Is(err, net.ErrClosed) {
		t.Fatal("socket not closed", err)
	}
	if len(requests) != 0 {
		t.Fatal("request slot leaked")
	}
}
