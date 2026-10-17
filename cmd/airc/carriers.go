package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

// nativeDialBudget bounds the native connection when other carriers are
// known, leaving the rest of a one-shot command's deadline for them.
const nativeDialBudget = 4 * time.Second

// carrierCache is the server's advertised carrier list, saved after a
// successful connection so a later command can fall back when native IRC is
// unreachable.
type carrierCache struct {
	Server   string   `json:"server"`
	Carriers []string `json:"carriers"`
}

func carrierCachePath(opt options) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(serverKey(opt)))
	return filepath.Join(dir, "carriers-"+hex.EncodeToString(sum[:8])+".json"), nil
}

func loadCarrierCache(opt options) []protocol.Carrier {
	path, err := carrierCachePath(opt)
	if err != nil {
		return nil
	}
	data, err := atomicfile.ReadBounded(path, 64<<10)
	if err != nil {
		return nil
	}
	var cache carrierCache
	if json.Unmarshal(data, &cache) != nil || cache.Server != serverKey(opt) {
		return nil
	}
	var carriers []protocol.Carrier
	for _, s := range cache.Carriers {
		c, err := protocol.ParseCarrier(s)
		if err != nil {
			return nil
		}
		carriers = append(carriers, c)
	}
	if !describesPrimary(opt, carriers) {
		return nil
	}
	return carriers
}

// describesPrimary reports whether an advertised list's ports and paths are
// those of the server at opt's address: its first entry, aircd itself, must be
// the endpoint opt reaches. Through a tunnel or port mapping they are not, and
// the advertised ports could belong to another server on this host.
func describesPrimary(opt options, carriers []protocol.Carrier) bool {
	if len(carriers) == 0 || carriers[0].Name != "irc" {
		return false
	}
	first := carriers[0]
	if opt.unix != "" {
		return first.Path == opt.unix
	}
	_, port, err := net.SplitHostPort(opt.addr)
	return err == nil && first.Path == "" && port == strconv.Itoa(first.Port) && first.TLS == usesTLS(opt)
}

// rememberCarriers saves (or, when the server advertises none, forgets) the
// carrier list. It never fails the command that learned it.
func rememberCarriers(opt options, carriers []protocol.Carrier) {
	path, err := carrierCachePath(opt)
	if err != nil {
		return
	}
	if len(carriers) == 0 || !describesPrimary(opt, carriers) {
		if len(carriers) == 0 {
			_ = os.Remove(path)
		}
		return
	}
	cache := carrierCache{Server: serverKey(opt)}
	for _, c := range carriers {
		cache.Carriers = append(cache.Carriers, c.String())
	}
	if old := loadCarrierCache(opt); slices.Equal(old, carriers) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = saveLocalState(path, cache, atomicfile.SyncNone)
}

// learnCarriers records what a native connection advertised in 005 CARRIERS.
func learnCarriers(opt options, client *irc.Client) {
	value, ok := client.Features()["CARRIERS"]
	if !ok {
		rememberCarriers(opt, nil)
		return
	}
	if carriers, err := protocol.ParseCarriers(value); err == nil {
		rememberCarriers(opt, carriers)
	}
}

// fallbackEndpoints are the carriers to try after native IRC at opt's
// address: --carriers (AIRC_CARRIERS) when given, else the saved list. The
// native entry for opt's own address is dropped; it was just tried.
func fallbackEndpoints(opt options) ([]fallback.Endpoint, error) {
	var endpoints []fallback.Endpoint
	if opt.carriers != "" {
		var err error
		if endpoints, err = fallback.ParseEndpoints(opt.carriers); err != nil {
			return nil, err
		}
	} else {
		endpoints = fallback.FromCarriers(loadCarrierCache(opt))
	}
	host := primaryHost(opt)
	return slices.DeleteFunc(endpoints, func(e fallback.Endpoint) bool {
		if e.Name != "irc" || e.Host != "" && e.Host != host {
			return false
		}
		if opt.unix != "" {
			return e.Path == opt.unix
		}
		_, port, _ := net.SplitHostPort(opt.addr)
		return e.Path == "" && strconv.Itoa(e.Port) == port
	}), nil
}

// primaryHost is where advertised ports are: the host of opt's address, or
// loopback for a Unix-socket server (its gateway binds 127.0.0.1).
func primaryHost(opt options) string {
	if opt.unix != "" {
		return "127.0.0.1"
	}
	host, _, err := net.SplitHostPort(opt.addr)
	if err != nil {
		return opt.addr
	}
	return host
}

// fallbackClient runs requests as cfg's author (its nickname and saved
// account token) over endpoints.
func fallbackClient(opt options, cfg irc.Config, endpoints []fallback.Endpoint) *fallback.Client {
	c := &fallback.Client{Endpoints: endpoints, Host: primaryHost(opt), Nick: cfg.Nick, Token: cfg.IdentityToken, Password: cfg.AccessToken, TLSConfig: cfg.TLSConfig, KeepAlive: cfg.KeepAlive, ProbeTimeout: cfg.ProbeTimeout}
	if c.TLSConfig != nil && opt.tlsServerName == "" {
		c.TLSConfig = c.TLSConfig.Clone()
		c.TLSConfig.ServerName = "" // each endpoint's own host
	}
	return c
}

// nativeTunnels are the gateway carriers that carry native IRC itself, so a
// one-shot command can race them beside aircd's own port.
func nativeTunnels(opt options) []fallback.Endpoint {
	endpoints, err := fallbackEndpoints(opt)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(endpoints, func(e fallback.Endpoint) bool { return !fallback.NativeCarrier(e.Name) })
}

// dialNativeRacing registers an ephemeral native session over aircd's own
// port or a gateway tunnel, whichever connects first (the tunnel that last
// answered in native IRC's place leads), and remembers which answered.
func dialNativeRacing(ctx context.Context, opt options, cfg irc.Config, tunnels []fallback.Endpoint) (*irc.Client, error) {
	endpoints := append([]fallback.Endpoint{primaryEndpoint(opt)}, tunnels...)
	last := loadLastCarrier(opt)
	if i := slices.IndexFunc(endpoints, func(e fallback.Endpoint) bool { return e.String() == last }); i > 0 {
		endpoints = slices.Concat(endpoints[i:i+1], endpoints[:i], endpoints[i+1:])
	}
	fb := fallbackClient(opt, cfg, endpoints)
	fb.NativeConnectTimeout = nativeDialBudget
	conn, err := fb.Connect(ctx)
	if err != nil {
		return nil, raceError(opt, err)
	}
	answeredBy(opt, reached{last: last}, conn.Endpoint)
	return conn.Native(), nil
}

// fallbackFor reports whether a failed native connection may fall back, and
// the client to do it with. Only connection-level failures fall back; a
// definite refusal (a rejected nickname or token) is the answer. The native
// tunnels were raced with aircd's port already, so only the gateway's
// request carriers remain.
func fallbackFor(opt options, dialErr error) (*fallback.Client, bool) {
	if !fallback.Unreachable(dialErr) {
		return nil, false
	}
	endpoints, err := fallbackEndpoints(opt)
	if err != nil {
		return nil, false
	}
	endpoints = slices.DeleteFunc(endpoints, func(e fallback.Endpoint) bool { return fallback.NativeCarrier(e.Name) })
	if len(endpoints) == 0 {
		return nil, false
	}
	cfg, err := dialConfig(opt)
	if err != nil {
		return nil, false
	}
	return fallbackClient(opt, cfg, endpoints), true
}

// learnFromGateway saves the list a gateway advertises after a command first
// reached the server through explicitly configured carriers.
func learnFromGateway(ctx context.Context, opt options, c *fallback.Client, answered fallback.Endpoint) {
	if opt.carriers == "" || answered.Host != "" || loadCarrierCache(opt) != nil {
		return
	}
	probe := *c
	probe.Endpoints, probe.Attempted = []fallback.Endpoint{answered}, nil
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if carriers, _, err := probe.Carriers(ctx); err == nil {
		rememberCarriers(opt, carriers)
	}
}

// primaryEndpoint is native IRC at opt's own address.
func primaryEndpoint(opt options) fallback.Endpoint {
	if opt.unix != "" {
		return fallback.Endpoint{Carrier: protocol.Carrier{Name: "irc", Path: opt.unix}}
	}
	_, port, _ := net.SplitHostPort(opt.addr)
	p, _ := strconv.Atoi(port)
	return fallback.Endpoint{Carrier: protocol.Carrier{Name: "irc", TLS: usesTLS(opt), Port: p}}
}

// reached is how a command reached the server: native IRC at opt's address,
// or another carrier's connection that won the race.
type reached struct {
	native *irc.Client
	conn   *fallback.Conn
	fb     *fallback.Client
	// endpoint is the carrier whose connection won: aircd's own port, a
	// native tunnel (native is set) or a request carrier (conn is set).
	endpoint fallback.Endpoint
	// last is the carrier remembered as the last to answer, when one was.
	last string
}

// raceMode orders a command's carriers.
type raceMode int

const (
	// nativeOrder races native IRC first, then the others in order.
	nativeOrder raceMode = iota
	// preferLast leads with the carrier that last answered in native IRC's
	// place, within lastCarrierTTL; otherwise native IRC races first.
	preferLast
	// nativeLead gives native IRC two quiet seconds before other carriers are dialled,
	// for a command that reads more over native IRC than elsewhere.
	nativeLead
	// nativeFirst dials another carrier only once native IRC has failed,
	// for a command that needs native IRC when it is reachable.
	nativeFirst
)

// connectRacing connects for a command that can run over any carrier. With
// other carriers known it races native IRC against them (native first, or
// the carrier that last answered when preferLast and that was within
// lastCarrierTTL), so a silent native port costs a fraction of a second.
// Without them it is dialOneShot.
func connectRacing(ctx context.Context, opt options, mode raceMode) (reached, error) {
	if opt.session != nil && opt.session.client != nil {
		select {
		case <-opt.session.client.Done():
			opt.session.client = nil // closed: race for a new connection
		default:
			return reached{native: opt.session.client}, nil
		}
	}
	fb, last, err := racingClient(opt, mode)
	if err != nil {
		return reached{}, err
	}
	if fb == nil {
		client, err := dialOneShot(ctx, opt)
		return reached{native: client}, err
	}
	r := reached{last: last}
	conn, err := fb.Connect(ctx)
	if err != nil {
		return r, raceError(opt, err)
	}
	r.endpoint = conn.Endpoint
	if conn.Endpoint != primaryEndpoint(opt) && !fallback.NativeCarrier(conn.Endpoint.Name) {
		r.conn, r.fb = conn, fb
		return r, nil
	}
	// Native IRC, at aircd's own port or through a gateway tunnel: the
	// command runs as it always has.
	r.native = conn.Native()
	learnCarriers(opt, r.native)
	if opt.session != nil {
		opt.session.client = r.native
	}
	answeredBy(opt, r, conn.Endpoint)
	return r, nil
}

// racingClient is a fallback client over native IRC and the server's other
// carriers, ordered for mode, and the carrier last remembered; nil when no
// other carrier is known.
func racingClient(opt options, mode raceMode) (*fallback.Client, string, error) {
	endpoints, err := fallbackEndpoints(opt)
	if err != nil {
		return nil, "", fmt.Errorf("--carriers: %w", err)
	}
	if len(endpoints) == 0 {
		return nil, "", nil
	}
	cfg, err := dialConfig(opt)
	if err != nil {
		return nil, "", err
	}
	last := loadLastCarrier(opt)
	endpoints = append([]fallback.Endpoint{primaryEndpoint(opt)}, endpoints...)
	if i := slices.IndexFunc(endpoints, func(e fallback.Endpoint) bool { return e.String() == last }); mode == preferLast && i > 0 {
		endpoints = slices.Concat(endpoints[i:i+1], endpoints[:i], endpoints[i+1:])
	}
	fb := fallbackClient(opt, cfg, endpoints)
	fb.NativeConnectTimeout = nativeDialBudget
	switch mode {
	case nativeLead:
		fb.Stagger = 2 * time.Second
	case nativeFirst:
		// Native IRC finishes connecting, or fails, before any other
		// carrier is dialled.
		fb.Stagger = nativeDialBudget + time.Second
	}
	return fb, last, nil
}

// readRacing runs a read that gives the same answer over any carrier: on
// native IRC as chatRequestWithContext would, when native IRC wins the race,
// or else on the winning carrier, which reports the carrier that answered.
func readRacing(ctx context.Context, opt options, capability string, native func(context.Context, *irc.Client) error, other func(context.Context, *fallback.Conn) (fallback.Endpoint, error)) error {
	r, err := connectRacing(ctx, opt, preferLast)
	if err != nil {
		return err
	}
	if r.native != nil {
		defer closeOneShot(opt, r.native)
		defer closeOnCancel(ctx, r.native)()
		if !r.native.Supports(capability) {
			return fmt.Errorf("this aircd lacks %s; upgrade when safe", capability)
		}
		return native(ctx, r.native)
	}
	answered, err := other(ctx, r.conn)
	if err != nil {
		return raceError(opt, err)
	}
	answeredBy(opt, r, answered)
	return nil
}

// raceError reports a race that found no carrier as the native failure
// followed by the others, and a native refusal in native IRC's own words.
func raceError(opt options, err error) error {
	primary := primaryEndpoint(opt)
	var refusal *fallback.Refusal
	if errors.As(err, &refusal) && refusal.Endpoint == primary && refusal.Err != nil {
		return refusal.Err
	}
	var all *fallback.Unavailable
	if !errors.As(err, &all) {
		return err
	}
	var native error
	others := &fallback.Unavailable{MayHavePosted: all.MayHavePosted}
	for _, a := range all.Attempts {
		if a.Endpoint == primary && native == nil {
			native = a.Err
		} else {
			others.Attempts = append(others.Attempts, a)
		}
	}
	switch {
	case native == nil:
		return err
	case len(others.Attempts) == 0:
		return native
	}
	return fmt.Errorf("%v; fallback: %w", native, others)
}

// lastCarrierTTL is how long the carrier that last answered for an
// unreachable native IRC is tried first.
const lastCarrierTTL = 10 * time.Minute

type lastCarrier struct {
	Server  string    `json:"server"`
	Carrier string    `json:"carrier"`
	At      time.Time `json:"at"`
}

func lastCarrierPath(opt options) (string, error) {
	path, err := carrierCachePath(opt)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "last-"+filepath.Base(path)), nil
}

// loadLastCarrier is the carrier that last answered for this server, if it
// did so within lastCarrierTTL. A missing or damaged file is no answer.
func loadLastCarrier(opt options) string {
	path, err := lastCarrierPath(opt)
	if err != nil {
		return ""
	}
	data, err := atomicfile.ReadBounded(path, 4<<10)
	if err != nil {
		return ""
	}
	var last lastCarrier
	if json.Unmarshal(data, &last) != nil || last.Server != serverKey(opt) {
		return ""
	}
	if age := time.Since(last.At); age < 0 || age > lastCarrierTTL {
		return ""
	}
	return last.Carrier
}

// answeredBy remembers the carrier that answered when it was not native IRC,
// and forgets a remembered one once native IRC answers again. The time saved
// is when a carrier first answered in place of native IRC, so a carrier tried
// first because it is remembered does not keep itself first forever. It
// never fails the command.
func answeredBy(opt options, r reached, e fallback.Endpoint) {
	path, err := lastCarrierPath(opt)
	if err != nil {
		return
	}
	if e == primaryEndpoint(opt) {
		if r.last != "" {
			_ = os.Remove(path)
		}
		return
	}
	if e.String() == r.last {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = saveLocalState(path, lastCarrier{Server: serverKey(opt), Carrier: e.String(), At: time.Now().UTC()}, atomicfile.SyncNone)
}

// carrierName is the carrier to report for an answer: empty for native IRC
// at opt's own address, or when no race chose one.
func carrierName(opt options, e fallback.Endpoint) string {
	if e.Name == "" || e == primaryEndpoint(opt) {
		return ""
	}
	return e.String()
}

// dialPrimary connects natively, within nativeDialBudget when other carriers
// are known.
func dialPrimary(ctx context.Context, opt options) (*irc.Client, error) {
	endpoints, err := fallbackEndpoints(opt)
	if err != nil {
		return nil, fmt.Errorf("--carriers: %w", err)
	}
	if len(endpoints) > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, nativeDialBudget)
		defer cancel()
	}
	return dialOneShot(ctx, opt)
}
