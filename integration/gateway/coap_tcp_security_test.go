//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/tcp/coder"
)

func TestCoAPTCPRequiresCSMBeforeApplicationFrames(t *testing.T) {
	f := newCoAPTCPFixture(t)
	t.Run("application-before-csm-closes", func(t *testing.T) {
		request := core.Request{Version: 1, RequestID: "tcp-before-csm", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "before CSM"}
		frame, err := json.Marshal(frameRequest{Version: 1, CorrelationID: "tcp-before-csm", Profile: "plain", Request: f.credentials.token(t, request, false)})
		if err != nil {
			t.Fatal(err)
		}
		wire := rawCoAPTCPMessage(t, codes.POST, "/v1/coap/rpc", frame, true)
		if len(wire) > 1152 {
			t.Fatalf("pre-CSM probe exceeds the independent frame limit: %d", len(wire))
		}
		conn := dialRawCoAPTCP(t, f, wire)
		defer conn.Close()
		assertCoAPTCPCloses(t, conn)
		rpcNoJobs(t, f.config.StateDir)
		if records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name); len(records) != 0 {
			t.Fatalf("pre-CSM native intake: %+v", records)
		}
	})
	t.Run("valid-csm-then-unauthenticated-frame", func(t *testing.T) {
		conn := dialRawCoAPTCP(t, f, rawCoAPTCPMessage(t, codes.CSM, "", nil, false), rawCoAPTCPMessage(t, codes.POST, "", nil, false))
		defer conn.Close()
		deadline := time.Now().Add(5 * time.Second)
		for {
			response := readRawCoAPTCPMessage(t, conn, deadline)
			if response.Code == codes.BadRequest {
				break
			}
		}
		rpcNoJobs(t, f.config.StateDir)
	})
	t.Run("invalid-csm-option-closes", func(t *testing.T) {
		msg := pool.NewMessage(t.Context())
		msg.SetCode(codes.CSM)
		msg.SetOptionString(message.OptionID(1), "x")
		invalid, err := msg.MarshalWithEncoder(coder.DefaultCoder)
		if err != nil {
			t.Fatal(err)
		}
		conn := dialRawCoAPTCP(t, f, invalid)
		defer conn.Close()
		assertCoAPTCPCloses(t, conn)
		rpcNoJobs(t, f.config.StateDir)
	})
	t.Run("missing-csm-idle-closes", func(t *testing.T) {
		conn := dialRawCoAPTCP(t, f)
		defer conn.Close()
		assertCoAPTCPCloses(t, conn)
		rpcNoJobs(t, f.config.StateDir)
	})
	request := core.Request{Version: 1, RequestID: "tcp-before-csm", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "before CSM"}
	accepted := coapTCPAccepted(t, f, request, false)
	records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name)
	if accepted.SendAttempts != 1 || len(records) != 1 || records[0].ID != accepted.MessageID || records[0].Message != request.Message {
		t.Fatalf("valid-CSM matched control accepted=%+v records=%+v", accepted, records)
	}
}

func rawCoAPTCPMessage(t *testing.T, code codes.Code, path string, body []byte, content bool) []byte {
	t.Helper()
	msg := pool.NewMessage(t.Context())
	msg.SetCode(code)
	if path != "" {
		if err := msg.SetPath(path); err != nil {
			t.Fatal(err)
		}
	}
	if content {
		msg.SetContentFormat(message.AppJSON)
	}
	if len(body) > 0 {
		msg.SetBody(bytes.NewReader(body))
	}
	encoded, err := msg.MarshalWithEncoder(coder.DefaultCoder)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func dialRawCoAPTCP(t *testing.T, f coapTCPFixture, frames ...[]byte) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: time.Second}, Config: &tls.Config{RootCAs: f.cert.Client.RootCAs, ServerName: "localhost", MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{coapTCPALPN}}}).DialContext(ctx, "tcp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if _, err := conn.Write(frame); err != nil {
			conn.Close()
			t.Fatal(err)
		}
	}
	return conn
}

func assertCoAPTCPCloses(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(6500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatalf("CoAPS TCP peer remained open until read deadline")
			}
			return
		}
		if n == 0 {
			return
		}
	}
}

func readRawCoAPTCPMessage(t *testing.T, conn net.Conn, deadline time.Time) message.Message {
	t.Helper()
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	first := []byte{0}
	if _, err := io.ReadFull(conn, first); err != nil {
		t.Fatal(err)
	}
	extLen := int(first[0] >> 4)
	extBytes := 0
	switch extLen {
	case 13:
		extBytes = 1
	case 14:
		extBytes = 2
	case 15:
		extBytes = 4
	}
	header := append([]byte(nil), first...)
	extBytes += 1 + int(first[0]&15) // Code and Token are part of DecodeHeader input.
	if extBytes > 0 {
		extra := make([]byte, extBytes)
		if _, err := io.ReadFull(conn, extra); err != nil {
			t.Fatal(err)
		}
		header = append(header, extra...)
	}
	var parsed coder.MessageHeader
	if _, err := coder.DefaultCoder.DecodeHeader(header, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.MessageLength < uint32(len(header)) || parsed.MessageLength > 1152 {
		t.Fatalf("invalid CoAP TCP message length=%d header=%d", parsed.MessageLength, len(header))
	}
	body := make([]byte, int(parsed.MessageLength)-len(header))
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatal(err)
	}
	wire := append(header, body...)
	decoded := message.Message{Options: make(message.Options, 0, 32)}
	if _, err := coder.DefaultCoder.Decode(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
