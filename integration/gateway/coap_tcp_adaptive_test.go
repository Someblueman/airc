//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapClient "github.com/plgd-dev/go-coap/v3/tcp/client"
	"github.com/plgd-dev/go-coap/v3/tcp/coder"
)

func TestCoAPSTCPAdaptiveHistoryAndObserve(t *testing.T) {
	for _, maximum := range []uint32{64, 256, 512, 1100} {
		t.Run(fmt.Sprint(maximum), func(t *testing.T) {
			f := newCoAPTCPFixture(t)
			native := seedCoAPTCPAdaptiveHistory(t, f)
			_ = f.conn.Close()
			var blocks atomic.Int64
			f.conn = dialCoAPTCPWithLimit(t, f.address, f.cert, maximum, func(_ *coapClient.Conn, m *pool.Message) (bool, error) {
				wire, err := m.MarshalWithEncoder(coder.DefaultCoder)
				if err != nil || len(wire) > int(maximum) {
					return true, fmt.Errorf("received frame bytes=%d maximum=%d: %w", len(wire), maximum, err)
				}
				if m.Code() == codes.Content && m.HasOption(message.Block2) {
					blocks.Add(1)
				}
				return false, nil
			})
			room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
			response := coapTCPRPC(t, f, core.Request{Version: 1, RequestID: "adaptive-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 16}, true)
			if response.Result == nil || response.Result.Page == nil || !reflect.DeepEqual(response.Result.Page.Messages, native) {
				t.Fatalf("adaptive history changed independent native records: %+v", response)
			}
			if blocks.Load() < 3 {
				t.Fatalf("history did not require multiple wire blocks: %d", blocks.Load())
			}
			statusClient := nativeStatusClient(t, f.config.Bindings[0].Address)
			baseline := nativeObserverCount(t, statusClient)
			observation, notifications, _ := startCoAPSTCPObserveRoute(t, f, core.Request{Version: 1, RequestID: "adaptive-watch", Operation: core.Watch, Target: room, Start: "retained", Limit: 16}, maximum < 1100)
			select {
			case ready := <-notifications:
				var marker struct {
					Event string `json:"event"`
				}
				if ready.err != nil || !ready.hasObserve || ready.observe != 0 || json.Unmarshal(ready.body, &marker) != nil || marker.Event != "ready" {
					t.Fatalf("adaptive ready: %+v", ready)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("adaptive Observe ready timeout")
			}
			before := blocks.Load()
			page := coapTCPObserveFrame(t, notifications, time.After(10*time.Second))
			if page.Event != "page" || page.Sequence != 1 || page.Page == nil || !reflect.DeepEqual(page.Page.Messages, native) {
				t.Fatalf("adaptive Observe changed whole native page: %+v", page)
			}
			if maximum < 1100 && blocks.Load()-before < 3 {
				t.Fatalf("Observe did not use small physical blocks: %d", blocks.Load()-before)
			}
			waitNativeObserverCount(t, statusClient, baseline+1)
			author := dial(t, f.config.Bindings[0].Address, "adaptive-live", "", false, true)
			liveBody := strings.Repeat("adaptive live notification 🙂 ", 80)
			if err := author.SendWithID(room.Name, liveBody, "adaptive-live-message"); err != nil {
				t.Fatal(err)
			}
			_ = author.Close()
			liveNative := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.From == "adaptive-live" && e.Message == liveBody })
			live := coapTCPObserveFrame(t, notifications, time.After(10*time.Second))
			if live.Sequence != 2 || live.Page == nil || len(live.Page.Messages) != 1 || live.Page.Messages[0].ID != liveNative.ID || live.Page.Messages[0].Message != liveBody {
				t.Fatalf("adaptive live notification differs from native: %+v", live)
			}
			heartbeat := coapTCPObserveFrame(t, notifications, time.After(10*time.Second))
			if heartbeat.Sequence != 3 || heartbeat.Page == nil || len(heartbeat.Page.Messages) != 0 || heartbeat.Page.Cursor == "" {
				t.Fatalf("adaptive heartbeat: %+v", heartbeat)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			err := observation.Cancel(ctx)
			cancel()
			if err != nil {
				t.Fatalf("adaptive Observe cancellation: %v", err)
			}
			waitNativeObserverCountAtMost(t, statusClient, baseline)
		})
	}
}

func TestCoAPSTCPAdaptiveCSMLoweringDuringHistory(t *testing.T) {
	f := newCoAPTCPFixture(t)
	native := seedCoAPTCPAdaptiveHistory(t, f)
	_ = f.conn.Close()
	var lowered atomic.Bool
	var laterBlocks atomic.Int64
	f.conn = dialCoAPTCPWithLimit(t, f.address, f.cert, 512, func(c *coapClient.Conn, m *pool.Message) (bool, error) {
		wire, err := m.MarshalWithEncoder(coder.DefaultCoder)
		maximum := 512
		if lowered.Load() {
			maximum = 256
		}
		if err != nil || len(wire) > maximum {
			return true, fmt.Errorf("received frame bytes=%d maximum=%d: %w", len(wire), maximum, err)
		}
		if m.Code() != codes.Content || !m.HasOption(message.Block2) {
			return false, nil
		}
		if lowered.CompareAndSwap(false, true) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			csm := c.AcquireMessage(ctx)
			defer c.ReleaseMessage(csm)
			csm.SetCode(codes.CSM)
			csm.SetOptionUint32(message.TCPMaxMessageSize, 256)
			return false, c.Session().WriteMessage(csm)
		}
		laterBlocks.Add(1)
		return false, nil
	})
	response := coapTCPRPC(t, f, core.Request{Version: 1, RequestID: "adaptive-lowered-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 16}, true)
	if !lowered.Load() || laterBlocks.Load() < 3 || response.Result == nil || response.Result.Page == nil || !reflect.DeepEqual(response.Result.Page.Messages, native) {
		t.Fatalf("CSM lowering did not preserve whole native history: later=%d response=%+v", laterBlocks.Load(), response)
	}
}

func seedCoAPTCPAdaptiveHistory(t *testing.T, f coapTCPFixture) []*irc.MessageMetadata {
	t.Helper()
	author := dial(t, f.config.Bindings[0].Address, "adaptive-author", "", false, true)
	defer author.Close()
	for i := 0; i < 6; i++ {
		body := strings.Repeat(fmt.Sprintf("adaptive-%d-🙂 ", i), 160)
		if err := author.SendWithID("#gateway-fixture", body, fmt.Sprintf("adaptive-seed-%d", i)); err != nil {
			t.Fatal(err)
		}
		next(t, f.observer, func(e *irc.MessageEvent) bool { return e.From == "adaptive-author" && e.Message == body })
	}
	return nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
}
