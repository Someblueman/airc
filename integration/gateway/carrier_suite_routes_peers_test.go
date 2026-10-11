//go:build darwin || linux

package gateway_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"github.com/Someblueman/airc/internal/testirc"
	"github.com/Someblueman/airc/internal/testsnmp"
	"github.com/Someblueman/airc/internal/testsyslog"
	"github.com/Someblueman/airc/internal/testtls"
)

// addRouteListener enables l on a free loopback port of network (tcp or udp).
func addRouteListener(t *testing.T, c *config.Config, l config.Listener, network string) {
	t.Helper()
	var probe io.Closer
	if network == "tcp" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		probe, l.Address = listener, listener.Addr().String()
	} else {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		probe, l.Address = conn, conn.LocalAddr().String()
	}
	_ = probe.Close()
	l.Enabled = true
	c.Listeners = append(c.Listeners, l)
}

// routeClosed checks that the gateway ended conn without answering: by
// closing the socket or, when the refusal may come from TLS, by any error.
func routeClosed(t *testing.T, conn net.Conn, tlsAlert bool) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var one [1]byte
	_, err := conn.Read(one[:])
	if err == nil {
		t.Fatal("refused peer received an application response")
	}
	if n, ok := err.(net.Error); ok && n.Timeout() {
		t.Fatal("refusal stalled", err)
	}
	if !tlsAlert && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatal("not a terminal socket closure", err)
	}
}

// --- TLS syslog (RFC 5425): a pinned client certificate is the route.

var syslogRefusals = []string{"missing_certificate", "unknown_certificate", "wrong_server_name", "TLS12", "malformed", "oversize_declaration", "binary_body", "oversize_body", "truncated"}

type syslogRoute struct {
	e      *routeEnv
	cert   testtls.Certificate
	client tls.Certificate
	events int
}

func newSyslogRoute(t *testing.T, e *routeEnv, c *config.Config) routeProvider {
	p := &syslogRoute{e: e, cert: testtls.New(t), client: syslogClientCertificate(t)}
	addRouteListener(t, c, p.listener(t, c, "syslog_tls"), "tcp")
	return p
}

// listener serves TLS to the pinned client, whose grant binds alice.
func (p *syslogRoute) listener(t *testing.T, c *config.Config, protocol string) config.Listener {
	l := routeListener(protocol)
	l.TLSCertFile, l.TLSKeyFile = p.cert.CertPath, p.cert.KeyPath
	pin := sha256.Sum256(p.client.Certificate[0])
	l.Syslog = &config.Syslog{Routes: []config.SyslogRoute{{CertificateSHA256: hex.EncodeToString(pin[:]), BoundAuthor: config.BoundAuthor{Nick: "alice", AccountTokenFile: p.e.aliceTokenFile(t, c, protocol+"-peer")}, Target: routeTarget}}}
	return l
}

// trusted is the pinned client's TLS configuration.
func (p *syslogRoute) trusted() *tls.Config {
	c := p.cert.Client.Clone()
	c.ServerName = "localhost"
	c.Certificates = []tls.Certificate{p.client}
	return c
}

func (p *syslogRoute) open(t *testing.T, c *tls.Config) (*tls.Conn, error) {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", p.e.address, c)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	t.Cleanup(func() { _ = conn.NetConn().Close() })
	return conn, nil
}

// dial connects the pinned client over TLS 1.3 without ALPN.
func (p *syslogRoute) dial(t *testing.T) *tls.Conn {
	t.Helper()
	conn, err := p.open(t, p.trusted())
	if err != nil {
		t.Fatal(err)
	}
	if state := conn.ConnectionState(); state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != "" {
		t.Fatalf("TLS state %+v", state)
	}
	return conn
}

func (p *syslogRoute) connect(t *testing.T) routePeer {
	return &syslogPeer{route: p, conn: p.dial(t)}
}

// record is an RFC 5424 record whose hostname is not its author.
func (p *syslogRoute) record(event, body string) []byte {
	return []byte("<34>1 2026-10-07T00:00:00Z forged-author app 42 " + event + " - " + body)
}

// requestID is the gateway's identity of a record on the route.
func (p *syslogRoute) requestID(t *testing.T, raw []byte) string {
	t.Helper()
	data, err := json.Marshal(struct {
		Protocol, Realm string
		Target          core.Target
		Record          []byte
	}{"syslog_tls/content-v1", p.e.config.Realm, routeTarget, raw})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (p *syslogRoute) refuse(t *testing.T, kind string) (string, bool) {
	client := p.trusted()
	switch kind {
	case "missing_certificate":
		client.Certificates = nil
	case "unknown_certificate":
		client.Certificates = []tls.Certificate{syslogClientCertificate(t)}
	case "wrong_server_name":
		client.ServerName = "wrong.invalid"
	case "TLS12":
		client.MinVersion, client.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	}
	authRefusal := slices.Contains(syslogRefusals[:4], kind)
	raw := p.record("refused", "must not post")
	conn, err := p.open(t, client)
	if err != nil {
		if !authRefusal {
			t.Fatal("a valid peer could not reach framing", err)
		}
		return p.requestID(t, raw), true
	}
	wire := octetCounted(raw)
	switch kind {
	case "malformed":
		wire = []byte("5 wrong")
	case "oversize_declaration":
		wire = []byte("32769 ")
	case "binary_body":
		raw = p.record("refused", "\xff")
		wire = octetCounted(raw)
	case "oversize_body":
		raw = p.record("refused", strings.Repeat("x", 4097))
		wire = octetCounted(raw)
	case "truncated":
		wire = []byte("200 <34>1 -")
	}
	_, _ = conn.Write(wire)
	if kind == "truncated" {
		_ = conn.NetConn().Close()
		return "", true
	}
	routeClosed(t, conn, authRefusal)
	return p.requestID(t, raw), true
}

// partial leaves half a record open.
func (p *syslogRoute) partial(t *testing.T) {
	if _, err := p.dial(t).Write([]byte("200 <34>1 -")); err != nil {
		t.Fatal(err)
	}
}

func octetCounted(raw []byte) []byte {
	return append([]byte(strconv.Itoa(len(raw))+" "), raw...)
}

func writeSyslog(t *testing.T, conn *tls.Conn, raw []byte) {
	t.Helper()
	wire := octetCounted(raw)
	if n, err := conn.Write(wire); err != nil || n != len(wire) {
		t.Fatal("incomplete TLS write", n, err)
	}
}

type syslogPeer struct {
	route *syslogRoute
	conn  *tls.Conn
}

func (s *syslogPeer) deliver(t *testing.T, text string) routeItem {
	s.route.events++
	raw := s.route.record(fmt.Sprint("event", s.route.events), text)
	writeSyslog(t, s.conn, raw)
	return routeItem{id: s.route.requestID(t, raw), text: text, wire: [][]byte{raw}}
}

func (s *syslogPeer) replay(t *testing.T, item routeItem) {
	for _, raw := range item.wire {
		writeSyslog(t, s.conn, raw)
	}
}

func (s *syslogPeer) close() { _ = s.conn.NetConn().Close() }

func syslogClientCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(99), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}
}

// --- RFC 5848 signed syslog: a provisioned origin signs blocks of records.

var signedSyslogRefusals = []string{"uncovered", "missing_certificate", "altered_record", "altered_signature", "stale_record", "future_record", "count_bound"}

type signedSyslogRoute struct {
	*syslogRoute
	origin  testsyslog.Origin
	started time.Time
	signer  [32]byte
	// block and number are the next signature block counter and message
	// number of the origin's one reboot session.
	block, number int
}

func newSignedSyslogRoute(t *testing.T, e *routeEnv, c *config.Config) routeProvider {
	now := time.Now().UTC().Truncate(time.Second)
	p := &signedSyslogRoute{syslogRoute: &syslogRoute{e: e, cert: testtls.New(t), client: syslogClientCertificate(t)}, started: now, number: 1}
	p.origin = testsyslog.New(t, now.Add(-time.Hour), now.Add(time.Hour))
	cert, err := x509.ParseCertificate(p.origin.DER)
	if err != nil {
		t.Fatal(err)
	}
	p.signer = sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	l := p.listener(t, c, "syslog_signed")
	l.SignedSyslog = &config.SignedSyslog{Routes: []config.SignedSyslogRoute{{CertificateDER: base64.StdEncoding.EncodeToString(p.origin.DER), Hostname: "signed.example", BoundAuthor: config.BoundAuthor{Nick: "alice", AccountTokenFile: e.aliceTokenFile(t, c, "origin")}, Target: routeTarget}}}
	addRouteListener(t, c, l, "tcp")
	return p
}

func signedHeader(at time.Time) string {
	return "<110>1 " + at.Format(time.RFC3339) + " signed.example app 1 audit "
}

// dial connects and sends the origin's certificate block.
func (p *signedSyslogRoute) dial(t *testing.T, certificate bool) *tls.Conn {
	t.Helper()
	conn := p.syslogRoute.dial(t)
	if certificate {
		payload := p.started.Format(time.RFC3339) + " C " + base64.StdEncoding.EncodeToString(p.origin.DER)
		writeSyslog(t, conn, p.origin.Sign(t, signedHeader(time.Now())+fmt.Sprintf(`[ssign-cert VER="0121" RSID="1" SG="0" SPRI="110" TPBL="%d" INDEX="1" FLEN="%d" FRAG="%s"]`, len(payload), len(payload), payload)))
	}
	return conn
}

func (p *signedSyslogRoute) connect(t *testing.T) routePeer {
	return &signedSyslogPeer{route: p, conn: p.dial(t, true)}
}

// sign is a signature block numbered counter covering records from first.
func (p *signedSyslogRoute) sign(t *testing.T, counter, first int, records ...[]byte) []byte {
	t.Helper()
	hashes := make([]string, len(records))
	for i, raw := range records {
		digest := sha256.Sum256(raw)
		hashes[i] = base64.StdEncoding.EncodeToString(digest[:])
	}
	return p.origin.Sign(t, signedHeader(time.Now())+fmt.Sprintf(`[ssign VER="0121" RSID="1" SG="0" SPRI="110" GBC="%d" FMN="%d" CNT="%d" HB="%s"]`, counter, first, len(records), strings.Join(hashes, " ")))
}

func (p *signedSyslogRoute) requestID(t *testing.T, number int, raw []byte) string {
	t.Helper()
	data, err := json.Marshal(struct {
		Protocol       string
		Signer         [32]byte
		Hostname       string
		Reboot, Number uint64
		Record         []byte
	}{"syslog_signed/record-v1", p.signer, "signed.example", 1, uint64(number), raw})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// refuse sends a record the origin's signature does not cover, with the next
// block counter and message number, which a refusal must not consume.
func (p *signedSyslogRoute) refuse(t *testing.T, kind string) (string, bool) {
	conn := p.dial(t, kind != "missing_certificate")
	stamp := time.Now()
	switch kind {
	case "stale_record":
		stamp = stamp.Add(-time.Duration(p.e.config.Retry.LifetimeSeconds+61) * time.Second)
	case "future_record":
		stamp = stamp.Add(120 * time.Second)
	}
	raw := []byte(signedHeader(stamp) + "- must not post")
	id := p.requestID(t, p.number, raw)
	block := p.sign(t, p.block, p.number, raw)
	switch kind {
	case "altered_record":
		raw = []byte(signedHeader(stamp) + "- altered record")
	case "altered_signature":
		block = []byte(strings.Replace(string(block), "app 1 audit", "app 1 forged", 1))
	}
	writeSyslog(t, conn, raw)
	switch kind {
	case "uncovered":
	case "count_bound":
		for range 99 {
			writeSyslog(t, conn, raw)
		}
	default:
		writeSyslog(t, conn, block)
	}
	routeClosed(t, conn, false)
	return id, true
}

// partial leaves a record its block has not yet covered.
func (p *signedSyslogRoute) partial(t *testing.T) {
	writeSyslog(t, p.dial(t, true), []byte(signedHeader(time.Now())+"- in flight at shutdown"))
}

type signedSyslogPeer struct {
	route *signedSyslogRoute
	conn  *tls.Conn
}

func (s *signedSyslogPeer) deliver(t *testing.T, text string) routeItem {
	p := s.route
	raw := []byte(signedHeader(time.Now()) + "- " + text)
	block := p.sign(t, p.block, p.number, raw)
	item := routeItem{id: p.requestID(t, p.number, raw), text: text, wire: [][]byte{raw, block}}
	p.block++
	p.number++
	s.replay(t, item)
	return item
}

func (s *signedSyslogPeer) replay(t *testing.T, item routeItem) {
	for _, raw := range item.wire {
		writeSyslog(t, s.conn, raw)
	}
}

func (s *signedSyslogPeer) close() { _ = s.conn.NetConn().Close() }

// One signature block covers a batch of records, each posted once.
func signedSyslogRouteWire(t *testing.T, e *routeEnv) {
	p := e.provider.(*signedSyslogRoute)
	conn := p.dial(t, true)
	texts := []string{e.text("batched"), e.text("batched")}
	var records [][]byte
	for _, text := range texts {
		records = append(records, []byte(signedHeader(time.Now())+"- "+text))
		writeSyslog(t, conn, records[len(records)-1])
	}
	writeSyslog(t, conn, p.sign(t, p.block, p.number, records...))
	for i, text := range texts {
		e.posted(t, routeItem{id: p.requestID(t, p.number+i, records[i]), text: text})
	}
	p.block++
	p.number += len(records)
}

// --- SNMPv3 traps (USM, HMAC-SHA-96): the engine and user are the route.

const (
	snmpEngine   = "8000000102030405"
	snmpPassword = "test-only-fixture-password"
)

var snmpRefusals = []string{"user", "engine", "context", "old_time", "old_boot", "exhausted_boot", "notification", "unknown_field", "missing_field", "binary_text", "bad_mac", "oversize"}

type snmpRoute struct {
	e           *routeEnv
	engine, key []byte
	// boots and clock are the engine's current time; request numbers traps.
	boots, clock, request int
}

func newSNMPRoute(t *testing.T, e *routeEnv, c *config.Config) routeProvider {
	p := &snmpRoute{e: e, boots: 2, clock: 1000}
	var err error
	if p.engine, err = hex.DecodeString(snmpEngine); err != nil {
		t.Fatal(err)
	}
	p.key = testsnmp.LocalizedKey(snmpPassword, p.engine, false)
	path := filepath.Join(filepath.Dir(c.StateDir), "snmp-localized-key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(p.key)), 0600); err != nil {
		t.Fatal(err)
	}
	// Alert carries one text field; Bound carries eight, to fill a body.
	var bound []config.SNMPField
	for i := range 8 {
		bound = append(bound, config.SNMPField{OID: fmt.Sprint("1.3.6.1.4.1.123.", 10+i), Name: fmt.Sprint("F", i), Type: "text", Required: true})
	}
	l := routeListener("snmp_v3")
	l.SNMP = &config.SNMP{Routes: []config.SNMPRoute{{EngineID: snmpEngine, User: "fixture", Authentication: snmpwire.HMACSHA196, AuthenticationKeyFile: path, ContextEngineID: snmpEngine, BoundAuthor: config.BoundAuthor{Nick: "alice", AccountTokenFile: e.aliceTokenFile(t, c, "snmp")}, Target: routeTarget,
		Notifications: []config.SNMPNotification{
			{OID: "1.3.6.1.4.1.123.1", Name: "Alert", Fields: []config.SNMPField{{OID: "1.3.6.1.4.1.123.2", Name: "Message", Type: "text", Required: true}}},
			{OID: "1.3.6.1.4.1.123.3", Name: "Bound", Fields: bound},
		}}}}
	addRouteListener(t, c, l, "udp")
	return p
}

// trap is an Alert notification whose Message is body.
func (p *snmpRoute) trap(body string, id int) testsnmp.Trap {
	return testsnmp.Trap{Engine: p.engine, User: "fixture", Boots: p.boots, Time: p.clock, MessageID: 42, RequestID: id, Flags: 1, Fields: []testsnmp.Field{
		{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 2, 1, 1, 3, 0}, Tag: 3, Application: true, Data: []byte{123}},
		{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0}, Tag: 6, Data: []byte{43, 6, 1, 4, 1, 123, 1}},
		{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 2}, Tag: 4, Data: []byte(body)},
	}}
}

// next advances the engine clock and numbers a new trap.
func (p *snmpRoute) next() int {
	p.clock++
	p.request++
	return p.request
}

func snmpRequestID(t *testing.T, p testsnmp.Trap) string {
	t.Helper()
	data, err := json.Marshal(struct {
		Domain, Engine, User string
		Boots, Time          uint32
		RequestID            int32
	}{"airc-snmp-v3", hex.EncodeToString(p.Engine), p.User, uint32(p.Boots), uint32(p.Time), int32(p.RequestID)})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (p *snmpRoute) dial(t *testing.T) *net.UDPConn {
	t.Helper()
	address, err := net.ResolveUDPAddr("udp", p.e.address)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func (p *snmpRoute) connect(t *testing.T) routePeer {
	return &snmpPeer{route: p, conn: p.dial(t)}
}

// send writes one datagram; the trap carrier never answers.
func (p *snmpRoute) send(t *testing.T, conn *net.UDPConn, packet []byte) {
	t.Helper()
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if n, err := conn.Write(packet); err != nil || n != len(packet) {
		t.Fatal(n, err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var reply [16]byte
	if n, err := conn.Read(reply[:]); err == nil {
		t.Fatal("unexpected trap acknowledgement", reply[:n])
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatal(err)
	}
}

func (p *snmpRoute) refuse(t *testing.T, kind string) (string, bool) {
	trap := p.trap("refused "+kind, 1000+p.request)
	switch kind {
	case "user":
		trap.User = "other"
	case "engine":
		trap.Engine = []byte{0x80, 0, 0, 1, 2, 3, 4, 6}
	case "context":
		trap.Context = "other"
	case "old_time":
		trap.Time = p.clock - 151
	case "old_boot":
		trap.Boots = p.boots - 1
	case "exhausted_boot":
		trap.Boots = 2147483647
	case "notification":
		trap.Fields[1].Data = []byte{43, 6, 1, 4, 1, 123, 9}
	case "unknown_field":
		trap.Fields[2].OID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 9}
	case "missing_field":
		trap.Fields = trap.Fields[:2]
	case "binary_text":
		trap.Fields[2].Data = []byte{255}
	}
	raw := testsnmp.Encode(t, trap, p.key)
	switch kind {
	case "bad_mac":
		raw[len(raw)-1] ^= 1
	case "oversize":
		raw = make([]byte, 8193)
	}
	p.send(t, p.dial(t), raw)
	return snmpRequestID(t, trap), false
}

func (p *snmpRoute) partial(*testing.T) {}

type snmpPeer struct {
	route *snmpRoute
	conn  *net.UDPConn
}

func (s *snmpPeer) deliver(t *testing.T, text string) routeItem {
	trap := s.route.trap(text, s.route.next())
	raw := testsnmp.Encode(t, trap, s.route.key)
	s.route.send(t, s.conn, raw)
	return routeItem{id: snmpRequestID(t, trap), text: "SNMP Alert\nMessage: " + text, wire: [][]byte{raw}}
}

func (s *snmpPeer) replay(t *testing.T, item routeItem) { s.route.send(t, s.conn, item.wire[0]) }

func (s *snmpPeer) close() { _ = s.conn.Close() }

// A Bound notification renders to exactly the 4096-byte body limit and posts
// whole; one byte more is refused, never truncated.
func snmpRouteWire(t *testing.T, e *routeEnv) {
	p := e.provider.(*snmpRoute)
	conn := p.dial(t)
	trap := p.trap("", p.next())
	trap.Fields = trap.Fields[:2]
	trap.Fields[1].Data = []byte{43, 6, 1, 4, 1, 123, 3}
	body, remaining := "SNMP Bound", 4096-len("SNMP Bound")
	for i := range 8 {
		name := fmt.Sprint("F", i)
		size := 512
		if i == 7 {
			size = remaining - len("\n"+name+": ")
		}
		remaining -= len("\n"+name+": ") + size
		value := strings.Repeat("x", size)
		trap.Fields = append(trap.Fields, testsnmp.Field{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 10 + i}, Tag: 4, Data: []byte(value)})
		body += "\n" + name + ": " + value
	}
	if len(body) != 4096 || remaining != 0 {
		t.Fatal("fixture size")
	}
	p.send(t, conn, testsnmp.Encode(t, trap, p.key))
	e.posted(t, routeItem{id: snmpRequestID(t, trap), text: body})
	trap.RequestID = p.next()
	trap.Time = p.clock
	trap.Fields[len(trap.Fields)-1].Data = append(trap.Fields[len(trap.Fields)-1].Data, 'x')
	p.send(t, conn, testsnmp.Encode(t, trap, p.key))
	over := snmpRequestID(t, trap)
	held := len(e.messages(t))
	e.posted(t, (&snmpPeer{route: p, conn: conn}).deliver(t, e.text("bound fence")))
	e.noJob(t, over)
	if messages := e.messages(t); len(messages) != held+1 {
		t.Fatal("an oversized body posted", messages[held:])
	}
}

// --- IRC bridge: the gateway is the TLS client of a scripted IRC server
// (testirc) that confirms SASL, account-tag and the routed channel's join.

var ircRefusals = []string{"unvouched", "client_tag", "bridge_echo", "stale", "unrouted_room"}

type ircRoute struct {
	e     *routeEnv
	peers chan *tls.Conn
	conn  *tls.Conn
	// starts is the gateway start conn belongs to; messages numbers msgids.
	starts, messages int
}

func newIRCRoute(t *testing.T, e *routeEnv, c *config.Config) routeProvider {
	p := &ircRoute{e: e, peers: make(chan *tls.Conn, 4)}
	// Served before the gateway starts, so its sessions are checked after
	// the gateway stops.
	b := testirc.Serve(t, filepath.Dir(c.StateDir), func(conn *tls.Conn) { p.peers <- conn })
	b.Enabled = true
	b.Accounts = []config.BridgeAccount{{External: "carol", Nick: "alice", AccountTokenFile: e.aliceTokenFile(t, c, "carol")}}
	b.Routes = []config.IRCRoute{{Room: "#external", Target: routeTarget, Operations: []core.Operation{core.Send}}}
	c.IRCBridges = []config.IRCBridge{b}
	return p
}

// session is the bridge's connection to the current gateway.
func (p *ircRoute) session(t *testing.T) *tls.Conn {
	t.Helper()
	if p.starts != p.e.starts {
		select {
		case p.conn = <-p.peers:
		case <-time.After(5 * time.Second):
			t.Fatal("the bridge did not register and join")
		}
		p.starts = p.e.starts
	}
	return p.conn
}

func (p *ircRoute) write(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(p.session(t), line+"\r\n"); err != nil {
		t.Fatal(err)
	}
}

// message is a channel message with the server's account tag (none when
// tag is empty), as the IRC server relays it.
func (p *ircRoute) message(tag, msgid, room, text string, at time.Time) string {
	tags := "msgid=" + msgid + ";time=" + at.UTC().Format("2006-01-02T15:04:05.000Z")
	if tag != "" {
		tags = tag + ";" + tags
	}
	return "@" + tags + " :alice!u@h PRIVMSG " + room + " :" + text
}

// deliver sends text from a services account and returns it as a route item.
func (p *ircRoute) deliver(t *testing.T, account, text string) routeItem {
	p.messages++
	msgid := fmt.Sprint("m", p.messages)
	line := p.message("account="+account, msgid, "#external", text, time.Now().Add(-time.Second))
	p.write(t, line)
	return routeItem{id: ircRequestID(msgid), text: text, wire: [][]byte{[]byte(line)}}
}

func (p *ircRoute) connect(t *testing.T) routePeer {
	p.session(t)
	return ircPeer{p}
}

func (p *ircRoute) refuse(t *testing.T, kind string) (string, bool) {
	p.messages++
	msgid, tag, room, at := fmt.Sprint("m", p.messages), "account=alice", "#external", time.Now().Add(-time.Second)
	switch kind {
	case "unvouched":
		tag = ""
	case "client_tag":
		tag = "+account=alice"
	case "bridge_echo":
		tag = "account=bridge-account"
	case "stale":
		at = at.Add(-2 * time.Minute)
	case "unrouted_room":
		room = "#elsewhere"
	}
	p.write(t, p.message(tag, msgid, room, "must not post", at))
	return ircRequestID(msgid), false
}

func (p *ircRoute) partial(*testing.T) {}

type ircPeer struct{ route *ircRoute }

func (i ircPeer) deliver(t *testing.T, text string) routeItem {
	return i.route.deliver(t, "alice", text)
}

// replay relays the same message ID again under another nickname: the
// services account, not the nickname, is the author.
func (i ircPeer) replay(t *testing.T, item routeItem) {
	i.route.write(t, strings.Replace(string(item.wire[0]), " :alice!", " :renamed!", 1))
}

func (ircPeer) close() {}

func ircRequestID(msgid string) string {
	h := sha256.New()
	for _, part := range []string{"airc-irc-event-v1", "network", msgid} {
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(part))))
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// A services account mapped to an AIRC account posts as that registered
// account, with its account ID, not as a guest.
func ircRouteWire(t *testing.T, e *routeEnv) {
	p := e.provider.(*ircRoute)
	item := p.deliver(t, "carol", e.text("mapped"))
	seen := e.observed.wait(t, item.text)
	if seen.From != "alice" || seen.Target != routeRoom {
		t.Fatalf("mapped account posted %+v", seen)
	}
	if r := waitReceipt(t, e.receipts, item.id, authorHeaders("alice", aliceToken)); r.State != core.Accepted || r.MessageID != seen.ID || r.SendAttempts != 1 {
		t.Fatalf("mapped receipt %+v", r)
	}
}
