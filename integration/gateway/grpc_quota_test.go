//go:build darwin || linux

package gateway_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/Someblueman/airc/api/gateway/v1"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCMultiplexedWatchQuotaAndReservedUnaryCapacity(t *testing.T) {
	limits := config.Defaults().Limits
	limits.Sessions, limits.SessionsPerAuthor = 3, 3
	limits.Workers = 1
	limits.Watches, limits.WatchesPerAuthor = 2, 2
	f := newGRPCGatewayFixtureWithLimits(t, limits)
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	var cancels []context.CancelFunc
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for i := 0; i < f.config.Limits.SessionsPerAuthor-1; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		cancels = append(cancels, cancel)
		stream, err := f.client.Watch(ctx, &v1.Request{RequestJson: grpcRequestJSON(t, core.Request{Version: 1, RequestID: fmt.Sprintf("grpc-quota-%d", i), Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stream.Recv(); err != nil {
			t.Fatal(err)
		}
	}
	waitNativeObserverCount(t, statusClient, baseline+len(cancels))
	ctx, cancel := grpcContext(t, f.token)
	defer cancel()
	stream, err := f.client.Watch(ctx, &v1.Request{RequestJson: grpcRequestJSON(t, core.Request{Version: 1, RequestID: "grpc-quota-extra", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8})})
	var denied *v1.WatchFrame
	if err == nil {
		denied, err = stream.Recv()
	}
	if status.Code(err) != codes.ResourceExhausted && (err != nil || denied.GetTerminal().GetError().GetCode() != string(core.CapacityExceeded)) {
		t.Fatalf("excess watch frame=%+v err=%v", denied, err)
	}
	capabilities, err := f.client.Capabilities(ctx, &v1.Empty{})
	if err != nil || capabilities.GetError() != nil || len(capabilities.GetResultJson()) == 0 {
		t.Fatalf("watch quota starved discovery: %+v %v", capabilities, err)
	}
	rpcNoJobs(t, f.config.StateDir)
	request := core.Request{Version: 1, RequestID: "grpc-reserved-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "send while read sessions are full\nwhole body 🙂"}
	accepted := grpcAccepted(t, f, request)
	seen := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == accepted.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message || accepted.SendAttempts != 1 {
		t.Fatal("reserved send lost native acceptance", accepted, seen)
	}
	if got := nativeObserverCount(t, statusClient); got != baseline+len(cancels) {
		t.Fatal("reads ended before reserved send acceptance", got, baseline+len(cancels))
	}
	for _, cancel := range cancels {
		cancel()
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	u := url.URL{Scheme: "file", Path: filepath.Join(f.config.StateDir, "journal.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&count); err != nil || count != 1 {
		t.Fatal("reads or denied request created a job beyond the accepted send", count, err)
	}
}
