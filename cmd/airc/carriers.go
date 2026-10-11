package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	c := &fallback.Client{Endpoints: endpoints, Host: primaryHost(opt), Nick: cfg.Nick, Token: cfg.IdentityToken, Password: cfg.AccessToken, TLSConfig: cfg.TLSConfig}
	if c.TLSConfig != nil && opt.tlsServerName == "" {
		c.TLSConfig = c.TLSConfig.Clone()
		c.TLSConfig.ServerName = "" // each endpoint's own host
	}
	return c
}

// fallbackFor reports whether a failed native connection may fall back, and
// the client to do it with. Only connection-level failures fall back; a
// definite refusal (a rejected nickname or token) is the answer.
func fallbackFor(opt options, dialErr error) (*fallback.Client, bool) {
	if !fallback.Unreachable(dialErr) {
		return nil, false
	}
	endpoints, err := fallbackEndpoints(opt)
	if err != nil || len(endpoints) == 0 {
		return nil, false
	}
	cfg, err := dialConfig(opt)
	if err != nil {
		return nil, false
	}
	return fallbackClient(opt, cfg, endpoints), true
}

// unreachableEverywhere reports a fallback that found no carrier, keeping
// the native failure that started it.
func unreachableEverywhere(native, fallbackErr error) error {
	if fallback.Definite(fallbackErr) {
		return fallbackErr
	}
	return fmt.Errorf("%v; fallback: %w", native, fallbackErr)
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
