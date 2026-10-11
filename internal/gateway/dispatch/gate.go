package dispatch

import (
	"crypto/sha256"
	"errors"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// gate is one listener's author access policy, IRC style: which operations
// and targets authors may use, whether guests may, an optional access
// password (the IRC server PASS), and per-source and per-author flood limits.
// It is not an identity authority: AIRC decides who an author may be.
type gate struct {
	listener   string
	operations []core.Operation
	targets    []core.Target // nil: any target
	guests     bool
	password   *[sha256.Size]byte
	perSource  *buckets
	perAuthor  *buckets
}

// ForListener constructs a listener's dispatcher with the listener's author
// access policy.
func ForListener(journal *store.Store, reader *backend.Backend, cursors *auth.CursorCodec, l config.Listener) (*Dispatcher, error) {
	if !config.AuthorCarrier(l.Protocol) && !config.RouteCarrier(l.Protocol) {
		return nil, ErrUnsupported
	}
	d, err := newDispatcher(journal, reader, cursors, l.Operations)
	if err != nil {
		return nil, err
	}
	g := &gate{listener: l.ID, guests: l.GuestsAllowed()}
	if l.Targets != nil {
		g.targets = slices.Clone(l.Targets)
	}
	// Discovery and identify are always available to authors.
	g.operations = append(ListenerOperations(l, cursors != nil), core.CapabilitiesGet, core.AccountIdentify)
	if l.PasswordFile != "" {
		password, err := readPassword(l.PasswordFile)
		if err != nil {
			return nil, err
		}
		g.password = &password
	}
	limits := l.AccessLimits()
	g.perSource = newBuckets(limits.RequestsPerMinute)
	g.perAuthor = newBuckets(limits.AuthorRequestsPerMinute)
	d.gate = g
	return d, nil
}

// ListenerOperations are the operations authors may request on l: those it
// configures, or every implemented one and account registration when it
// configures none, without history, search and watches when the gateway has
// no cursor key. Discovery and identify are always added.
func ListenerOperations(l config.Listener, cursors bool) []core.Operation {
	all := append(ImplementedOperations(), core.AccountRegister)
	configured := l.Operations
	if len(configured) == 0 {
		configured = all
	}
	var operations []core.Operation
	for _, op := range all {
		if !slices.Contains(configured, op) || (op == core.HistoryRead || op == core.Search || op.IsWatch()) && !cursors {
			continue
		}
		operations = append(operations, op)
	}
	return operations
}

func readPassword(path string) ([sha256.Size]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [sha256.Size]byte{}, auth.ErrCredential
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if _, err := core.NewPassword(value); err != nil {
		return [sha256.Size]byte{}, errors.New("listener password file must hold one printable line")
	}
	return sha256.Sum256([]byte(value)), nil
}

// Source names a connection's client for AIRC's per-source limits: its IP
// address without the port, or "listener:<id>" for carriers without addresses.
func (d *Dispatcher) Source(addr net.Addr) string {
	var ip net.IP
	switch a := addr.(type) {
	case *net.TCPAddr:
		ip = a.IP
	case *net.UDPAddr:
		ip = a.IP
	}
	if ip == nil {
		return store.ListenerSource(d.listener())
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.String()
}

// RemoteSource is Source for an HTTP request's "host:port" remote address.
func (d *Dispatcher) RemoteSource(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if ip := net.ParseIP(host); err == nil && ip != nil {
		return d.Source(&net.TCPAddr{IP: ip})
	}
	return store.ListenerSource(d.listener())
}

func (d *Dispatcher) listener() string {
	if d == nil || d.gate == nil {
		return ""
	}
	return d.gate.listener
}

// AuthorAccess reports whether this dispatcher admits plain author requests.
func (d *Dispatcher) AuthorAccess() bool { return d != nil && d.gate != nil }

// buckets is a bounded set of per-key token buckets refilling perMinute
// tokens a minute, with a minute's worth of burst.
type buckets struct {
	mu        sync.Mutex
	perMinute float64
	entries   map[string]bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

const maxBuckets = 8192

func newBuckets(perMinute int) *buckets {
	return &buckets{perMinute: float64(perMinute), entries: map[string]bucket{}}
}

func (b *buckets) allow(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok {
		if len(b.entries) >= maxBuckets {
			b.prune(now)
		}
		e = bucket{tokens: b.perMinute, at: now}
	}
	if elapsed := now.Sub(e.at); elapsed > 0 {
		e.tokens = min(b.perMinute, e.tokens+elapsed.Minutes()*b.perMinute)
		e.at = now
	}
	if e.tokens < 1 {
		b.entries[key] = e
		return false
	}
	e.tokens--
	b.entries[key] = e
	return true
}

// prune forgets buckets that have refilled; if every bucket is still in use,
// it forgets arbitrary ones rather than grow without bound.
func (b *buckets) prune(now time.Time) {
	for key, e := range b.entries {
		if e.tokens+now.Sub(e.at).Minutes()*b.perMinute >= b.perMinute {
			delete(b.entries, key)
		}
	}
	for key := range b.entries {
		if len(b.entries) < maxBuckets {
			break
		}
		delete(b.entries, key)
	}
}
