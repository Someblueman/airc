//go:build darwin || linux

package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func rawQUICWatchFrame(t *testing.T, f rawQUICFixture, id string) []byte {
	t.Helper()
	return rawQUICFrame(t, rawQUICRequest(t, f, core.Request{Version: 1, RequestID: id, Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}, true, true))
}

func waitNativeLeaseGone(t *testing.T, statusClient *irc.Client, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if nativeObserverCount(t, statusClient) <= baseline {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("gateway watch lease remained: baseline=%d current=%d", baseline, nativeObserverCount(t, statusClient))
}

func TestRawQUICCancelReadAfterFINReleasesLeaseAndKeepsBidiUsable(t *testing.T) {
	f := newRawQUICFixture(t, false)
	socket := f.config.Upstream.Address
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	stream, err := f.conn.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(rawQUICWatchFrame(t, f, "quic-cancel-bidi")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	page := readRawQUICFrame(t, stream)
	if page.Event != "page" || page.Page == nil {
		t.Fatalf("raw QUIC bidi initial page=%+v", page)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	stream.CancelRead(0)
	waitNativeLeaseGone(t, statusClient, baseline)
	rawQUICBidiCall(t, f, core.Request{Version: 1, RequestID: "quic-cancel-bidi-capabilities", Operation: core.CapabilitiesGet}, true)
}

func TestRawQUICCancelReadAfterFINReleasesLeaseAndKeepsUniUsable(t *testing.T) {
	f := newRawQUICFixture(t, false)
	socket := f.config.Upstream.Address
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	out, err := f.conn.OpenUniStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write(rawQUICWatchFrame(t, f, "quic-cancel-uni")); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	in, err := f.conn.AcceptUniStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page := readRawQUICFrame(t, in)
	if page.Event != "page" || page.Page == nil {
		t.Fatalf("raw QUIC uni initial page=%+v", page)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	in.CancelRead(0)
	waitNativeLeaseGone(t, statusClient, baseline)
	rawQUICBidiCall(t, f, core.Request{Version: 1, RequestID: "quic-cancel-uni-capabilities", Operation: core.CapabilitiesGet}, true)
}
