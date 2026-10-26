//go:build darwin || linux

package gateway_test

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestFramedCLIFailsClosedAndBoundsAssembly(t *testing.T) {
	conn, credentials, _, stop := framedFixture(t, false)
	discovery := core.Request{Version: 1, RequestID: "discovery", Operation: core.CapabilitiesGet}
	signed := credentials.token(t, discovery, false)
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "wrong-profile", Profile: "unknown", Request: signed})
	if r := readFrame(t, conn); r.Error == nil || r.Error.Code != core.InvalidRequest {
		t.Fatal("unknown profile accepted", r)
	}
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "bad-token", Profile: "plain", Request: "{}"})
	if r := readFrame(t, conn); r.Error == nil || r.Error.Code != core.InvalidRequest {
		t.Fatal("missing authentication boundary", r)
	}
	// A wrapper cannot add an unsigned cursor/target or duplicate version.
	for _, body := range []string{
		`{"version":1,"version":1,"correlation_id":"duplicate","request":{}}`,
		`{"version":1,"correlation_id":"override","request":{},"cursor":"unbound"}`,
		`{"version":1,"correlation_id":"bad handle","request":{}}`,
	} {
		c, err := net.DialTimeout("tcp", conn.RemoteAddr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], uint32(len(body)))
		_, err = c.Write(append(h[:], body...))
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		if _, err = c.Read(b[:]); err == nil {
			t.Fatal("invalid frame received response")
		}
		_ = c.Close()
	}
	for _, size := range []uint32{0, adapter.MaxRequestFrameBytes + 1} {
		c, err := net.DialTimeout("tcp", conn.RemoteAddr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], size)
		_, _ = c.Write(h[:])
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		if _, err = c.Read(b[:]); err == nil {
			t.Fatal("invalid length accepted")
		}
		_ = c.Close()
	}
	slow, err := net.DialTimeout("tcp", conn.RemoteAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// One byte starts the fixed assembly deadline; no attacker-selected allocation.
	_, _ = slow.Write([]byte{0})
	_ = slow.SetReadDeadline(time.Now().Add(7 * time.Second))
	began := time.Now()
	var b [1]byte
	if _, err = slow.Read(b[:]); err == nil || time.Since(began) > 6*time.Second {
		t.Fatal("unbounded partial-header assembly", err, time.Since(began))
	}
	_ = slow.Close()
	// Streaming an authenticated non-watch must fail before creating a stream.
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "not-watch", Profile: "plain", Request: credentials.token(t, discovery, true), Stream: true})
	if r := readFrame(t, conn); r.Error == nil || r.Error.Code != core.InvalidRequest {
		t.Fatal("non-watch streamed", r)
	}
	// A valid renewal still works after all rejected frames; no credential fallback.
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "valid", Profile: "plain", Request: credentials.token(t, discovery, true)})
	if r := readFrame(t, conn); r.Result == nil || r.Result.Capabilities == nil {
		t.Fatal("rejections damaged runtime", r)
	}
	truncated, err := net.DialTimeout("tcp", conn.RemoteAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(truncated, strings.NewReader("\x00\x00\x00\x10{"))
	_ = truncated.Close()
	watch := core.Request{Version: 1, RequestID: "watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1}
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "duplicate-active", Profile: "plain", Request: credentials.token(t, watch, true), Stream: true})
	if r := readFrame(t, conn); r.Page == nil {
		t.Fatal("watch did not start", r)
	}
	// Invalid-profile wrappers cannot emit a failure under a live handle.
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: "duplicate-active", Profile: "unknown", Request: signed})
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = conn.Read(b[:]); err == nil {
		t.Fatal("duplicate active handle received an ambiguous response")
	}
	stop()
}
