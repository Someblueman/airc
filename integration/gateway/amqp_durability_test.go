//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestAMQPDispositionWaitsForCommittedIntake(t *testing.T) {
	f := prepareAMQPFixture(t)
	f.start(t)
	peer := f.connect(t)
	db, err := sql.Open("sqlite", filepath.Join(f.config.StateDir, "journal.sqlite")+"?_pragma=busy_timeout(1000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lock, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = lock.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	r := core.Request{Version: 1, RequestID: "amqp-blocked-intake", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "AMQP blocked durable intake\n世界"}
	delivery := peer.submit(t, amqpPlainBody(t, r))
	type readResult struct {
		body []byte
		err  error
	}
	ack := make(chan readResult, 1)
	if err := peer.conn.SetReadDeadline(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			var header [8]byte
			_, err := io.ReadFull(peer.conn, header[:])
			var body []byte
			if err == nil {
				size := binary.BigEndian.Uint32(header[:4])
				if size < 8 || size > 65536 || header[4] != 2 || header[5] != 0 || header[6] != 0 || header[7] != 0 {
					err = io.ErrUnexpectedEOF
				} else {
					body = make([]byte, size-8)
					_, err = io.ReadFull(peer.conn, body)
				}
			}
			if err == nil && len(body) == 0 {
				continue
			} // Heartbeats never extend the absolute deadline.
			ack <- readResult{body: body, err: err}
			return
		}
	}()
	finished := false
	defer func() {
		if !finished {
			_ = peer.conn.NetConn().Close()
			<-ack
		}
	}()
	select {
	case result := <-ack:
		finished = true
		t.Fatal("disposition preceded committed intake", result.err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := lock.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false
	select {
	case result := <-ack:
		finished = true
		if result.err != nil {
			t.Fatal(result.err)
		}
		value := aqDecode(t, bytes.NewReader(result.body), 0)
		if value.descriptor != 0x15 || len(value.fields) < 5 || value.fields[1].number != uint64(delivery) || value.fields[3].number != 1 || value.fields[4].descriptor != 0x24 {
			t.Fatal(value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no committed disposition")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", r.RequestID).Scan(&count); err != nil || count != 1 {
		t.Fatal("Accepted without committed row", count, err)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	if event.From != "fixture-author" || event.Target != r.Target.Name {
		t.Fatal(event)
	}
	f.stop(t)
}
