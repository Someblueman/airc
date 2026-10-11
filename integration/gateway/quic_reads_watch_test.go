//go:build darwin || linux

package gateway_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRawQUICHistoryAndCapabilitiesMatchNative(t *testing.T) {
	f := newRawQUICFixture(t, false)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	seed := seedPinMutationMessage(t, f.config.Upstream.Address, room.Name, "raw-quic-history-parent")
	if seed == "" {
		t.Fatal("native seed did not return a message ID")
	}
	capabilities := rawQUICBidiCall(t, f, core.Request{Version: 1, RequestID: "quic-capabilities", Operation: core.CapabilitiesGet}, false)
	if capabilities.Result == nil || capabilities.Result.Capabilities == nil {
		t.Fatalf("raw QUIC capabilities=%+v", capabilities)
	}
	access := capabilities.Result.Capabilities
	if access.MaxRequestFrameBytes != 16<<10 || access.MaxResponseFrameBytes != 64<<10 || access.MaxHistoryBytes == 0 || !reflect.DeepEqual(access.WatchModes, []string{"long_poll", "quic_stream"}) {
		t.Fatalf("raw QUIC capability bounds/modes=%+v", access)
	}
	historyResponse := rawQUICBidiCall(t, f, core.Request{Version: 1, RequestID: "quic-history-read", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 8}, true)
	if historyResponse.Result == nil || historyResponse.Result.Page == nil {
		t.Fatalf("raw QUIC history=%+v", historyResponse)
	}
	want := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name)
	if len(want) == 0 || !reflect.DeepEqual(historyResponse.Result.Page.Messages, want) {
		t.Fatalf("raw QUIC history/native mismatch: got=%+v want=%+v", historyResponse.Result.Page.Messages, want)
	}
}

func TestRawQUICWatchRetainedLiveAndCancelRead(t *testing.T) {
	f := newRawQUICFixture(t, false)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := seedPinMutationMessage(t, f.config.Upstream.Address, room.Name, "raw-quic-watch-parent")
	stream, err := f.conn.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := core.Request{Version: 1, RequestID: "quic-watch", Operation: core.Watch, Target: room, Start: "retained", Limit: 8}
	if _, err := stream.Write(rawQUICFrame(t, rawQUICRequest(t, f, request, true, true))); err != nil {
		t.Fatal(err)
	}
	retained := readRawQUICFrame(t, stream)
	if retained.Event != "page" || retained.Page == nil || len(retained.Page.Messages) == 0 || retained.Page.Messages[0].ID != parent {
		t.Fatalf("raw QUIC retained watch=%+v", retained)
	}
	author := dial(t, f.config.Upstream.Address, "raw-quic-watch-author", "", false, true)
	defer author.Close()
	if err := author.SendWithID(room.Name, "raw QUIC live watch 🙂", "raw-quic-watch-live"); err != nil {
		t.Fatal(err)
	}
	live := readRawQUICFrame(t, stream)
	if live.Event != "page" || live.Page == nil || len(live.Page.Messages) == 0 || live.Page.Messages[len(live.Page.Messages)-1].ID == parent {
		t.Fatalf("raw QUIC live watch=%+v", live)
	}
	stream.CancelRead(0)
	_ = stream.Close()
	// A fresh correlated call on the same connection proves cancellation did not
	// strand the QUIC connection or its request slot.
	rawQUICBidiCall(t, f, core.Request{Version: 1, RequestID: "quic-after-watch", Operation: core.CapabilitiesGet}, true)
}

func TestRawQUICUniWatchRetainedLive(t *testing.T) {
	f := newRawQUICFixture(t, false)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := seedPinMutationMessage(t, f.config.Upstream.Address, room.Name, "raw-quic-uni-watch-parent")
	incoming, err := f.conn.OpenUniStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := core.Request{Version: 1, RequestID: "quic-uni-watch", Operation: core.Watch, Target: room, Start: "retained", Limit: 8}
	if _, err := incoming.Write(rawQUICFrame(t, rawQUICRequest(t, f, request, true, true))); err != nil {
		t.Fatal(err)
	}
	if err := incoming.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	responseStream, err := f.conn.AcceptUniStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	retained := readRawQUICFrame(t, responseStream)
	if retained.Event != "page" || retained.Page == nil || len(retained.Page.Messages) == 0 || retained.Page.Messages[0].ID != parent {
		t.Fatalf("raw QUIC uni retained watch=%+v", retained)
	}
	author := dial(t, f.config.Upstream.Address, "raw-quic-uni-watch-author", "", false, true)
	defer author.Close()
	if err := author.SendWithID(room.Name, "raw QUIC uni live 🙂", "raw-quic-uni-watch-live"); err != nil {
		t.Fatal(err)
	}
	live := readRawQUICFrame(t, responseStream)
	if live.Event != "page" || live.Page == nil || len(live.Page.Messages) == 0 || live.Page.Messages[len(live.Page.Messages)-1].ID == parent {
		t.Fatalf("raw QUIC uni live watch=%+v", live)
	}
	responseStream.CancelRead(0)
}
