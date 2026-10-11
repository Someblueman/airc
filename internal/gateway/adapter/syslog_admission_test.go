//go:build darwin || linux

package adapter

import (
	"context"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/syslogwire"
	"strings"
	"testing"
	"time"
)

func syslogRecord(t *testing.T, body string) syslogwire.Record {
	t.Helper()
	r, e := syslogwire.Parse([]byte("<34>1 - spoofed-author app proc id - " + body))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestSyslogDurableExactReplayAndJournalReopen(t *testing.T) {
	f := newSyslogAuthorFixture(t)
	record := syslogRecord(t, "exact body\n世界")
	target := f.target
	request, e := syslogRequest(f.c.Realm, target, record)
	if e != nil {
		t.Fatal(e)
	}
	slots := make(chan struct{}, 1)
	upstream := ""
	for i := 0; i < 3; i++ {
		result, e := admitSyslog(t.Context(), f.d, f.author, f.c.Realm, target, record, slots)
		if e != nil || !result.Durable || len(slots) != 0 {
			t.Fatal(result, e)
		}
		job, e := f.lookup(t, request.RequestID)
		if e != nil || job.Author != (core.Author{Nick: syslogTestAuthor}) || job.Request.Message != "exact body\n世界" || job.Request.Target == nil || *job.Request.Target != target || job.Result.SendAttempts != 0 || upstream != "" && upstream != job.UpstreamID {
			t.Fatal(job, e)
		}
		upstream = job.UpstreamID
		if i == 0 {
			if e := f.s.Close(); e != nil {
				t.Fatal(e)
			}
			f.s, e = store.Open(t.Context(), f.c)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = f.s.Close() })
			f.d = f.listener(t, config.Listener{ID: "syslog", Protocol: "syslog_tls", Operations: []core.Operation{core.Send}})
		}
	}
	changed := syslogRecord(t, "different event")
	r, e := syslogRequest(f.c.Realm, target, changed)
	if e != nil || r.RequestID == request.RequestID {
		t.Fatal("record identity collapsed", e)
	}
	for _, body := range []string{"", "\x00", "\xff", strings.Repeat("x", 4097), "changed\r\nbody"} {
		if _, e := syslogRequest(f.c.Realm, target, syslogRecord(t, body)); e == nil {
			t.Fatalf("unsupported body %q", body)
		}
	}
}

func TestSyslogAdmissionCapacityAndAuthor(t *testing.T) {
	f := newSyslogAuthorFixture(t)
	// An unloaded grant (no nickname) is not an author.
	foreign := auth.RouteAuthor{}
	record := syslogRecord(t, "must not admit")
	target := f.target
	request, e := syslogRequest(f.c.Realm, target, record)
	if e != nil {
		t.Fatal(e)
	}
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	if _, e = admitSyslog(t.Context(), f.d, f.author, f.c.Realm, target, record, slots); e == nil || len(slots) != 1 {
		t.Fatal("stole occupied slot", e)
	}
	<-slots
	if _, e = admitSyslog(t.Context(), f.d, foreign, f.c.Realm, target, record, slots); e == nil || len(slots) != 0 {
		t.Fatal("foreign authority admitted", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = admitSyslog(ctx, f.d, f.author, f.c.Realm, target, record, slots); e == nil || len(slots) != 0 {
		t.Fatal("cancelled request admitted", e)
	}
	request.Operation = core.HistoryRead
	if _, e = f.d.AdmitAs(t.Context(), "route:syslog", f.author, request, time.Now()); e == nil {
		t.Fatal("read crossed send-only boundary")
	}
	if _, e = f.lookup(t, request.RequestID); e == nil {
		t.Fatal("refused request has job")
	}
}
