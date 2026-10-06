//go:build darwin || linux

package gateway_test

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSocketPairFrameFailureAndFreshChildRecovery(t *testing.T) {
	binaryPath := currentGatewayBinary(t)
	for _, variant := range []string{"partial-header", "partial-body", "malformed-json", "oversized", "zero-length"} {
		t.Run(variant, func(t *testing.T) {
			socket, c, credentials, observer := stdioFixtureConfig(t)
			observer.Close()
			request := core.Request{Version: 1, RequestID: "pair-failed-" + variant, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must never be admitted"}
			body, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: "failed-frame", Profile: "signed", Envelope: credentials.token(t, request, false)})
			if err != nil {
				t.Fatal(err)
			}
			if variant == "malformed-json" {
				body = []byte("{")
			}
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], uint32(len(body)))
			payload := append(header[:], body...)
			switch variant {
			case "partial-header":
				payload = header[:2]
			case "partial-body":
				payload = payload[:len(payload)-1]
			case "oversized":
				binary.BigEndian.PutUint32(header[:], adapter.MaxRequestFrameBytes+1)
				payload = header[:]
			case "zero-length":
				binary.BigEndian.PutUint32(header[:], 0)
				payload = header[:]
			}
			conn, stop, _ := startParentFD(t, binaryPath, c, "socketpair", syscall.SIGINT, true)
			if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if n, err := conn.Write(payload); err != nil || n != len(payload) {
				t.Fatal("failed-frame write", n, err)
			}
			if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
				t.Fatal(err)
			}
			if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var output [1]byte
			if n, err := conn.Read(output[:]); n != 0 || !errors.Is(err, io.EOF) {
				t.Fatal("failed frame produced data or did not close", n, err)
			}
			stop()
			journalURL := url.URL{Scheme: "file", Path: filepath.Join(c.StateDir, "journal.sqlite"), RawQuery: "mode=ro"}
			db, err := sql.Open("sqlite", journalURL.String())
			if err != nil {
				t.Fatal(err)
			}
			var jobs int
			err = db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&jobs)
			closeErr := db.Close()
			if err != nil || closeErr != nil || jobs != 0 {
				t.Fatal("failed frame reached intake", jobs, err, closeErr)
			}
			healthy, join, _ := startParentFD(t, binaryPath, c, "socketpair", syscall.SIGTERM, false)
			framedAuthenticatedSendAndBoundedResume(t, healthy, credentials, socket, true)
			join()
		})
	}
}
