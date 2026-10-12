//go:build linux

package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestUnixPacketCompleteRequestBoundaryAndAncillaryRefusal(t *testing.T) {
	valid := []byte(`{"version":1,"correlation_id":"record","profile":"plain","request":{}}`)
	exact := append(append([]byte(nil), valid...), bytes.Repeat([]byte(" "), MaxRequestFrameBytes-len(valid))...)
	for _, test := range []struct {
		name   string
		body   []byte
		rights bool
		valid  bool
	}{
		{"exact", exact, false, true},
		{"overflow", append(append([]byte(nil), exact...), ' '), false, false},
		{"empty", nil, false, false},
		{"ancillary", valid, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, client := unixPacketPair(t)
			if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			var control []byte
			if test.rights {
				file, err := os.Open("/dev/null")
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				control = syscall.UnixRights(int(file.Fd()))
			}
			n, oob, err := client.WriteMsgUnix(test.body, control, nil)
			if err != nil || n != len(test.body) || oob != len(control) {
				t.Fatal("send record", n, oob, err)
			}
			body, err := readUnixPacketRecord(server, true, make([]byte, MaxRequestFrameBytes))
			if test.valid {
				if err != nil || !bytes.Equal(body, test.body) {
					t.Fatal("complete record was not preserved", len(body), err)
				}
			} else if err == nil {
				t.Fatal("invalid record reached dispatch", len(body))
			}
		})
	}
}

func TestUnixPacketWholeResponseBoundary(t *testing.T) {
	server, client := unixPacketPair(t)
	base := FrameResponse{Version: 1, CorrelationID: "response", Event: "end", Reason: ""}
	empty, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Reason = strings.Repeat("a", MaxResponseFrameBytes-len(empty)-len(`,"reason":""`))
	// Reason is omitted when empty; include its field overhead exactly once.
	encoded, err := json.Marshal(base)
	if err != nil || len(encoded) != MaxResponseFrameBytes {
		t.Fatal("wrong boundary fixture", len(encoded), err)
	}
	if err := writeUnixPacketRecord(server, base); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, MaxResponseFrameBytes+1)
	n, control, flags, _, err := client.ReadMsgUnix(data, nil)
	if err != nil || n != MaxResponseFrameBytes || control != 0 || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 || !bytes.Equal(data[:n], encoded) {
		t.Fatal("response was not one complete record", n, control, flags, err)
	}
	base.Reason += "a"
	if err := writeUnixPacketRecord(server, base); !errors.Is(err, store.ErrCapacity) {
		t.Fatal("response overflow accepted", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	n, _, _, _, err = client.ReadMsgUnix(data, nil)
	var timeout net.Error
	if n > 0 || !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatal("oversize response emitted a record or failed without timeout", n, err)
	}
}

func TestUnixPacketWholeWritePressureHasBoundedDeadline(t *testing.T) {
	server, _ := unixPacketPair(t)
	if err := server.SetWriteBuffer(64 << 10); err != nil {
		t.Fatal(err)
	}
	response := FrameResponse{Version: 1, CorrelationID: "pressure", Event: "end", Reason: ""}
	base, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	response.Reason = strings.Repeat("a", MaxResponseFrameBytes-len(base)-len(`,"reason":""`))
	started := time.Now()
	for i := 0; i < 8; i++ {
		err = writeUnixPacketRecord(server, response)
		if err != nil {
			var timeout net.Error
			if !errors.As(err, &timeout) || !timeout.Timeout() {
				t.Fatal("whole record pressure failed without the write deadline", err)
			}
			if elapsed := time.Since(started); elapsed > 5*time.Second {
				t.Fatal("whole record write exceeded its bounded deadline", elapsed)
			}
			return
		}
	}
	t.Fatal("unread peer did not exert bounded whole-record write pressure")
}
