//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"database/sql"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSyslogNativeRefusalsLeaveNoJobOrPost(t *testing.T) {
	for _, kind := range []string{"missing_certificate", "unknown_certificate", "wrong_server_name", "TLS12", "malformed", "oversize_declaration", "binary_body", "oversize_body", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			f := prepareSyslogFixture(t)
			f.start(t)
			client := f.cert.Client.Clone()
			client.ServerName = "localhost"
			client.Certificates = []tls.Certificate{f.client}
			switch kind {
			case "missing_certificate":
				client.Certificates = nil
			case "unknown_certificate":
				client.Certificates = []tls.Certificate{syslogFixtureClient(t)}
			case "wrong_server_name":
				client.ServerName = "wrong.invalid"
			case "TLS12":
				client.MinVersion = tls.VersionTLS12
				client.MaxVersion = tls.VersionTLS12
			}
			peer, e := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, client)
			authRefusal := kind == "missing_certificate" || kind == "unknown_certificate" || kind == "wrong_server_name" || kind == "TLS12"
			if e == nil {
				peer.SetDeadline(time.Now().Add(4 * time.Second))
				defer peer.NetConn().Close()
				raw := syslogNativeRecord("refused", "must not post")
				wire := append([]byte(strconv.Itoa(len(raw))+" "), raw...)
				switch kind {
				case "malformed":
					wire = []byte("5 wrong")
				case "oversize_declaration":
					wire = []byte("32769 ")
				case "binary_body":
					raw = syslogNativeRecord("refused", "\xff")
					wire = append([]byte(strconv.Itoa(len(raw))+" "), raw...)
				case "oversize_body":
					raw = syslogNativeRecord("refused", strings.Repeat("x", 4097))
					wire = append([]byte(strconv.Itoa(len(raw))+" "), raw...)
				case "truncated":
					wire = []byte("200 <34>1 -")
				}
				_, _ = peer.Write(wire)
				if kind == "truncated" {
					peer.NetConn().Close()
				} else {
					var b [1]byte
					_, e = peer.Read(b[:])
					if e == nil {
						t.Fatal("refused peer received application response")
					}
					if n, ok := e.(net.Error); ok && n.Timeout() {
						t.Fatal("refusal stalled", e)
					}
					if !authRefusal {
						if !errors.Is(e, io.EOF) && !errors.Is(e, io.ErrUnexpectedEOF) && !errors.Is(e, syscall.ECONNRESET) {
							t.Fatal("not a terminal socket closure", e)
						}
					}
				}
			} else if !authRefusal {
				t.Fatal("valid peer could not reach framing control", e)
			}
			f.stop(t)
			db, e := sql.Open("sqlite", filepath.Join(f.config.StateDir, "journal.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var count int
			if e := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&count); e != nil || count != 0 {
				t.Fatal("refusal created job", count, e)
			}
			if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 0 {
				t.Fatal("refusal posted", messages)
			}
		})
	}
}
