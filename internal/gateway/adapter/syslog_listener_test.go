//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testtls"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"
)

func syslogListenerFixture(t *testing.T, capacity int) (amqpAdmissionFixture, net.Listener, func() *tls.Conn, chan struct{}, context.CancelFunc, <-chan error) {
	t.Helper()
	f := newAMQPAdmissionFixture(t)
	cert := testtls.New(t)
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(7), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	raw, e := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	pin := sha256.Sum256(raw)
	a, e := auth.NewSyslogAuthenticator(f.v, config.Syslog{Routes: []config.SyslogRoute{{CertificateSHA256: hex.EncodeToString(pin[:]), Principal: f.identity.ID(), Target: f.c.Principals[0].Targets[0]}}})
	if e != nil {
		t.Fatal(e)
	}
	server, e := a.TLSConfig(cert.Server)
	if e != nil {
		t.Fatal(e)
	}
	listener, e := tls.Listen("tcp", "127.0.0.1:0", server)
	if e != nil {
		t.Fatal(e)
	}
	slots := make(chan struct{}, capacity)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- ServeSyslog(ctx, listener, a, nil, slots, 1, f.c.Realm, f.d, make(chan struct{}, 1))
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("cleanup owner unjoined")
		}
	})
	dial := func() *tls.Conn {
		t.Helper()
		client := cert.Client.Clone()
		client.ServerName = "localhost"
		client.Certificates = []tls.Certificate{{Certificate: [][]byte{raw}, PrivateKey: key}}
		c, e := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", listener.Addr().String(), client)
		if e != nil {
			t.Fatal(e)
		}
		c.SetDeadline(time.Now().Add(5 * time.Second))
		t.Cleanup(func() { closeMQTTConnection(c) })
		return c
	}
	return f, listener, dial, slots, cancel, done
}

func TestSyslogListenerIntakeAndJoinedShutdown(t *testing.T) {
	f, listener, dial, slots, cancel, done := syslogListenerFixture(t, 2)
	client := dial()
	record := syslogRecord(t, "listener exact 世界")
	if _, e := client.Write(append([]byte(strconv.Itoa(len(record.Raw))+" "), record.Raw...)); e != nil {
		t.Fatal(e)
	}
	r, e := syslogRequest(f.c.Realm, f.c.Principals[0].Targets[0], record)
	if e != nil {
		t.Fatal(e)
	}
	until := time.Now().Add(2 * time.Second)
	for {
		job, e := f.s.Lookup(t.Context(), f.identity, r.RequestID)
		if e == nil {
			if !job.Result.Durable {
				t.Fatal("not durable")
			}
			break
		}
		if time.Now().After(until) {
			t.Fatal("intake missing", e)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Principal admission is occupied by the first live connection.
	second := dial()
	other := syslogRecord(t, "refused second connection")
	_, _ = second.Write(append([]byte(strconv.Itoa(len(other.Raw))+" "), other.Raw...))
	second.SetReadDeadline(time.Now().Add(2 * time.Second))
	var b [1]byte
	if _, e := second.Read(b[:]); e == nil {
		t.Fatal("unexpected syslog response")
	} else if n, ok := e.(net.Error); ok && n.Timeout() {
		t.Fatal("capacity refusal stalled")
	}
	otherRequest, e := syslogRequest(f.c.Realm, f.c.Principals[0].Targets[0], other)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Lookup(t.Context(), f.identity, otherRequest.RequestID); e == nil {
		t.Fatal("second connection admitted")
	}
	address := listener.Addr().String()
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owners not joined")
	}
	if len(slots) != 0 {
		t.Fatal("socket slot leak")
	}
	replacement, e := net.Listen("tcp", address)
	if e != nil {
		t.Fatal("listener retained", e)
	}
	replacement.Close()
}

func TestSyslogPartialRecordDeadlineCannotRenew(t *testing.T) {
	f, _, dial, slots, cancel, done := syslogListenerFixture(t, 1)
	client := dial()
	start := time.Now()
	if _, e := client.Write([]byte("32768 ")); e != nil {
		t.Fatal(e)
	}
	writer := make(chan struct{})
	go func() {
		defer close(writer)
		for {
			if _, e := client.Write([]byte{'x'}); e != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	var b [1]byte
	_, e := client.Read(b[:])
	elapsed := time.Since(start)
	if e == nil || elapsed < 2800*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("renewed/early deadline %v %v", elapsed, e)
	}
	if n, ok := e.(net.Error); ok && n.Timeout() {
		t.Fatal("client timeout substituted for server closure")
	}
	closeMQTTConnection(client)
	select {
	case <-writer:
	case <-time.After(time.Second):
		t.Fatal("writer unjoined")
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("owner unjoined")
	}
	if len(slots) != 0 {
		t.Fatal("slot retained")
	}
	// No complete record means no queued journal operation.
	jobs, e := f.s.Ready(t.Context(), "", 1, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if len(jobs) != 0 {
		t.Fatal("partial record admitted")
	}
}

func TestSyslogGlobalSlotIncludesIncompleteTLS(t *testing.T) {
	_, listener, _, slots, cancel, done := syslogListenerFixture(t, 1)
	first, e := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	until := time.Now().Add(time.Second)
	for len(slots) != 1 {
		if time.Now().After(until) {
			t.Fatal("TLS peer bypassed socket slot")
		}
		time.Sleep(5 * time.Millisecond)
	}
	second, e := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	second.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, e := second.Read(b[:]); e == nil {
		t.Fatal("unexpected response")
	} else if n, ok := e.(net.Error); ok && n.Timeout() {
		t.Fatal("unclaimed peer retained")
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("unauthenticated TLS owner unjoined")
	}
	if len(slots) != 0 {
		t.Fatal("TLS socket slot leak")
	}
}
