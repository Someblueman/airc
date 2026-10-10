//go:build linux

package service

import (
	"encoding/json"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUnixPacketServiceOwnsRecordConnections(t *testing.T) {
	unixListenerLifecycle(t, "unixpacket", func(t *testing.T, path string) {
		conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Net: "unixpacket", Name: path})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			if n, _, _, _, err := conn.ReadMsgUnix(make([]byte, 1), nil); n != 0 || err != io.EOF {
				t.Errorf("accepted record connection remained after shutdown: %d %v", n, err)
			}
		})
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: "probe", Profile: "signed", Envelope: "untrusted"})
		if err != nil {
			t.Fatal(err)
		}
		if n, control, err := conn.WriteMsgUnix(body, nil, nil); err != nil || n != len(body) || control != 0 {
			t.Fatalf("write record: %d %d %v", n, control, err)
		}
		response := make([]byte, adapter.MaxResponseFrameBytes)
		n, control, flags, _, err := conn.ReadMsgUnix(response, nil)
		if err != nil || control != 0 || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 {
			t.Fatalf("read record: %d %d %d %v", n, control, flags, err)
		}
		var result adapter.FrameResponse
		if err := json.Unmarshal(response[:n], &result); err != nil || result.CorrelationID != "probe" || result.Error == nil || result.Error.Code != core.InvalidRequest {
			t.Fatalf("encrypted-only profile refusal: %+v %v", result, err)
		}
		// Keep this accepted connection open until the service shuts down.
	})
}

func TestUnixPacketBindFailureCleansSocketAndJournal(t *testing.T) {
	unixBindFailure(t, "unixpacket")
}
