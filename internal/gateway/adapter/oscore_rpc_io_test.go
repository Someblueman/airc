package adapter

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestOSCORERPCWireBoundsAndCancellation(t *testing.T) {
	for _, mode := range []string{"oversize", "zero", "truncated-header", "truncated-body", "cancel", "bad-frame"} {
		t.Run(mode, func(t *testing.T) {
			owner, peer := net.Pipe()
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() {
				done <- ServeOSCORERPC(ctx, owner, new(dispatch.Dispatcher), make(chan struct{}, 1), auth.OSCOREOwner{Realm: "fixture", Principal: "device", Binding: "backend"})
			}()
			joined := false
			defer func() {
				cancel()
				_ = peer.Close()
				if !joined {
					<-done
				}
			}()
			if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], 2)
			var request []byte
			switch mode {
			case "oversize":
				binary.BigEndian.PutUint32(header[:], MaxRequestFrameBytes+1)
				request = header[:]
			case "zero":
				request = make([]byte, 4)
			case "truncated-header":
				request = header[:2]
			case "truncated-body":
				request = append(header[:], '{')
			case "bad-frame":
				request = append(header[:], []byte("{}")...)
			case "cancel":
				cancel()
			}
			if len(request) != 0 {
				if _, err := peer.Write(request); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "bad-frame" {
				var reply [5]byte
				if _, err := io.ReadFull(peer, reply[:]); err != nil {
					t.Fatal(err)
				}
				length := binary.BigEndian.Uint32(reply[1:])
				if reply[0] != byte(codes.BadRequest) || length == 0 || length > MaxResponseFrameBytes {
					t.Fatal("unbounded or accepted malformed frame", reply)
				}
				body := make([]byte, length)
				if _, err := io.ReadFull(peer, body); err != nil {
					t.Fatal(err)
				}
				var response FrameResponse
				if json.Unmarshal(body, &response) != nil || response.Error == nil {
					t.Fatal("missing malformed-frame failure")
				}
			}
			_ = peer.Close()
			err := <-done
			joined = true
			if mode == "cancel" || mode == "bad-frame" {
				if err != nil {
					t.Fatal("clean close failed", err)
				}
			} else if err == nil {
				t.Fatal("invalid wire frame accepted")
			}
		})
	}
}
