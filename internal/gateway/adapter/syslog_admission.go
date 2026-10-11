package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/syslogwire"
	"time"
	"unicode/utf8"
)

func syslogRequest(realm string, target core.Target, record syslogwire.Record) (core.Request, error) {
	// Reparse exact owned bytes: detached Raw/Message fields cannot change authority.
	parsed, err := syslogwire.Parse(record.Raw)
	if err != nil || realm == "" {
		return core.Request{}, store.ErrInvalid
	}
	body := bytes.TrimPrefix(parsed.Message, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(body) || len(body) > 4096 {
		return core.Request{}, store.ErrInvalid
	}
	encoded, err := json.Marshal(struct {
		Protocol, Realm string
		Target          core.Target
		Record          []byte
	}{"syslog_tls/content-v1", realm, target, parsed.Raw})
	if err != nil {
		return core.Request{}, err
	}
	digest := sha256.Sum256(encoded)
	r := core.Request{Version: 1, RequestID: hex.EncodeToString(digest[:]), Operation: core.Send, Target: &target, Message: string(body)}
	original := r.Message
	if r.Normalize() != nil || r.Message != original {
		return core.Request{}, store.ErrInvalid
	}
	return r, nil
}

func admitSyslog(ctx context.Context, d *dispatch.Dispatcher, author auth.RouteAuthor, realm string, target core.Target, record syslogwire.Record, requests chan struct{}) (core.Result, error) {
	r, err := syslogRequest(realm, target, record)
	if err != nil {
		return core.Result{}, err
	}
	if cap(requests) < 1 || ctx.Err() != nil {
		return core.Result{}, store.ErrCapacity
	}
	select {
	case requests <- struct{}{}:
	default:
		return core.Result{}, store.ErrCapacity
	}
	defer func() { <-requests }()
	work, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := d.AdmitAs(work, dispatch.RouteSource(d.ListenerID()), author, r, time.Now())
	if err != nil {
		return core.Result{}, err
	}
	if result.Result == nil || !result.Result.Durable {
		return core.Result{}, store.ErrInvalid
	}
	return *result.Result, nil
}
