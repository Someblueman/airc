//go:build darwin || linux

package gateway_test

import (
	"database/sql"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSignedSyslogNativeRefusalsLeaveNoJobOrPost(t *testing.T) {
	for _, kind := range []string{"uncovered", "missing_certificate", "altered_record", "altered_signature", "stale_record", "future_record", "count_bound", "partial_shutdown"} {
		t.Run(kind, func(t *testing.T) {
			f := prepareSignedSyslogFixture(t)
			f.start(t)
			peer := f.connect(t)
			if kind != "missing_certificate" {
				f.certificate(t, peer)
			}
			stamp := time.Now()
			if kind == "stale_record" {
				stamp = stamp.Add(-time.Duration(f.config.Retry.LifetimeSeconds+61) * time.Second)
			}
			if kind == "future_record" {
				stamp = stamp.Add(120 * time.Second)
			}
			raw := []byte(signedSyslogHeader(stamp) + "- must not post")
			block := f.block(t, 0, 1, raw)
			if kind == "altered_record" {
				raw = []byte(signedSyslogHeader(stamp) + "- altered record")
			}
			if kind == "altered_signature" {
				block = []byte(strings.Replace(string(block), "app 1 audit", "app 1 forged", 1))
			}
			syslogNativeWrite(t, peer, raw)
			switch kind {
			case "uncovered", "partial_shutdown":
			case "count_bound":
				for i := 1; i < 100; i++ {
					syslogNativeWrite(t, peer, raw)
				}
			default:
				syslogNativeWrite(t, peer, block)
			}
			if kind != "partial_shutdown" {
				peer.SetReadDeadline(time.Now().Add(5 * time.Second))
				var one [1]byte
				_, err := peer.Read(one[:])
				if n, ok := err.(net.Error); ok && n.Timeout() {
					t.Fatal("refusal stalled", err)
				}
				if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, syscall.ECONNRESET) {
					t.Fatal("not a terminal socket closure", err)
				}
			}
			f.stop(t)
			peer.NetConn().Close()
			db, err := sql.Open("sqlite", filepath.Join(f.config.StateDir, "journal.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, table := range []string{"jobs", "signed_syslog_state", "replay"} {
				var count int
				if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatal("refusal created durable state", table, count, err)
				}
			}
			if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 0 {
				t.Fatal("refusal posted", messages)
			}
		})
	}
}
