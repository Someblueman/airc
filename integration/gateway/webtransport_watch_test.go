//go:build darwin || linux

package gateway_test

import (
	"context"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWebTransportBidiAndUniWatchDeliverRetainedAndLiveSignals(t *testing.T) {
	for _, uni := range []bool{false, true} {
		t.Run(map[bool]string{false: "bidi", true: "uni"}[uni], func(t *testing.T) {
			f := newWebTransportFixture(t)
			parentID := seedPinMutationMessage(t, f.config.Upstream.Address, "#gateway-fixture", "watch-parent")
			author := dial(t, f.config.Upstream.Address, "webtransport-watch-author", "", false, true)
			t.Cleanup(func() { _ = author.Close() })
			request := core.Request{Version: 1, RequestID: "watch-" + map[bool]string{false: "bidi", true: "uni"}[uni], Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
			frame := adapterFrameRequest(t, f, request, true)
			frame.Stream = true
			var reader interface{ Read([]byte) (int, error) }
			var closeStream func()
			if uni {
				out, err := f.session.OpenUniStreamSync(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := out.Write(webTransportFrame(t, frame)); err != nil {
					t.Fatal(err)
				}
				if err := out.Close(); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5e9)
				defer cancel()
				in, err := f.session.AcceptUniStream(ctx)
				if err != nil {
					t.Fatal(err)
				}
				reader, closeStream = in, func() { in.CancelRead(0) }
			} else {
				stream, err := f.session.OpenStreamSync(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := stream.Write(webTransportFrame(t, frame)); err != nil {
					t.Fatal(err)
				}
				reader, closeStream = stream, func() { stream.CancelRead(0); _ = stream.Close() }
			}
			t.Cleanup(closeStream)
			initial := readWebTransportFrame(t, reader)
			initialPage := initial.Page
			if initial.Event != "page" || initialPage == nil || len(initialPage.Messages) == 0 || initialPage.Messages[0].ID != parentID || initialPage.Cursor == "" {
				t.Fatalf("WebTransport %s initial watch=%+v", map[bool]string{false: "bidi", true: "uni"}[uni], initial)
			}
			triggerLiveSignal(t, author, "typing")
			live := readWebTransportFrame(t, reader)
			if live.Event != "page" || live.Page == nil || live.Page.Cursor != initialPage.Cursor || len(live.Page.Events) == 0 {
				t.Fatalf("WebTransport %s live watch=%+v", map[bool]string{false: "bidi", true: "uni"}[uni], live)
			}
		})
	}
}
