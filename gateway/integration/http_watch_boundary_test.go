package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
)

func TestHTTPLongPollQuietDeadlineAndCancellationJoin(t *testing.T) {
	t.Parallel()
	for _, protocol := range []int{1, 2} {
		for _, cancelled := range []bool{false, true} {
			t.Run(fmt.Sprintf("http%d/cancel=%t", protocol, cancelled), func(t *testing.T) {
				mode := "watch-noise"
				if cancelled {
					mode = "watch"
				}
				f := gatewayHTTPFixture(t, protocol == 2, mode)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/v1/watch?kind=room&target=%23gateway-fixture&limit=1", nil)
				if err != nil {
					t.Fatal(err)
				}
				setAuthorHeaders(request, f.token)
				type outcome struct {
					status, protocol int
					page             backend.HistoryPage
					err              error
				}
				done := make(chan outcome, 1)
				finished := make(chan struct{})
				t.Cleanup(func() {
					cancel()
					select {
					case <-finished:
					case <-time.After(5 * time.Second):
						t.Error("long-poll caller survived cleanup")
					}
				})
				began := time.Now()
				go func() {
					defer close(finished)
					response, err := f.server.Client().Do(request)
					if err != nil {
						done <- outcome{err: err}
						return
					}
					defer response.Body.Close()
					var page backend.HistoryPage
					err = json.NewDecoder(response.Body).Decode(&page)
					done <- outcome{response.StatusCode, response.ProtoMajor, page, err}
				}()
				select {
				case <-f.watchReady:
				case <-time.After(3 * time.Second):
					t.Fatal("long-poll never subscribed and snapshotted")
				}
				if cancelled {
					cancel()
				}
				select {
				case result := <-done:
					if cancelled {
						if result.err == nil {
							t.Fatal("cancelled long-poll returned success", result)
						}
					} else if result.err != nil || result.status != 200 || result.protocol != protocol || len(result.page.Messages) != 0 || result.page.Cursor != "" || result.page.Status != "ok" || result.page.Gap || time.Since(began) > 4*time.Second || time.Since(began) < 1500*time.Millisecond {
						t.Fatal("noise extended or corrupted HTTP quiet deadline", result, time.Since(began))
					}
				case <-time.After(4 * time.Second):
					t.Fatal("long-poll did not return within boundary")
				}
				select {
				case <-f.watchJoined:
				case <-time.After(2 * time.Second):
					t.Fatal("long-poll native lease survived return")
				}
			})
		}
	}
}
