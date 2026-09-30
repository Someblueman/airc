package server

import "testing"

func TestServerConfigBoundsResourceAllocations(t *testing.T) {
	srv := New(Config{MaxConnections: 1_000_000, MaxMessageSize: 1_000_000, OutboundQueue: 1_000_000, HistoryLimit: 1_000_000})
	if srv.cfg.MaxConnections != 128 || srv.cfg.MaxMessageSize != 4096 || srv.cfg.OutboundQueue != defaultOutboundQueue || srv.history.limit != maxHistoryMessages {
		t.Fatalf("unexpected bounded config: %#v history=%d", srv.cfg, srv.history.limit)
	}
}
