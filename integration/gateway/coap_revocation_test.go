//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapClient "github.com/plgd-dev/go-coap/v3/udp/client"
)

func TestCoAPRevocationDeniesFreshWorkAndExpiresExistingLease(t *testing.T) {
	f := newCoAPFixture(t)
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	started := time.Now()
	registration := coapObserveRegister(t, f, coapWatchRequest("coaps-revocation-watch"), true)
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	request, err := f.conn.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	request.SetToken(token)
	request.SetObserve(0)
	ready := make(chan struct{}, 1)
	observation, err := f.conn.DoObserve(request, func(response *pool.Message) {
		if _, err := response.ReadBody(); err == nil {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancelCoAPSObservation(observation)
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("CoAPS watch did not become active")
	}
	waitNativeObserverCount(t, f.config.Bindings[0].Address, baseline+1)
	revokeWebRTCPrincipal(t, f.config.StateDir, "device")
	assertCoAPSQLRevoked(t, f.config.StateDir)

	// Revocation blocks new work immediately, but the already-authorized read
	// grant remains valid until its fixed 15-second lease deadline.
	fresh := dialCoAPS(t, f.address, f.cert)
	assertCoAPRevoked(t, fresh, f.credentials, core.Request{Version: 1, RequestID: "coaps-revoked-capabilities", Operation: core.CapabilitiesGet})
	assertCoAPRevoked(t, fresh, f.credentials, coapWatchRequest("coaps-revoked-watch"))
	if got := nativeObserverCount(t, f.config.Bindings[0].Address); got != baseline+1 {
		t.Fatalf("revocation changed authorized lease count: observers=%d want=%d", got, baseline+1)
	}
	rpcNoJobs(t, f.config.StateDir)

	deadline := started.Add(18 * time.Second)
	for time.Now().Before(deadline) {
		if nativeObserverCount(t, f.config.Bindings[0].Address) <= baseline {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("revoked lease remained active past fixed deadline: observers=%d elapsed=%s", nativeObserverCount(t, f.config.Bindings[0].Address), time.Since(started))
}

func assertCoAPSQLRevoked(t *testing.T, stateDir string) {
	t.Helper()
	dsn := url.URL{Scheme: "file", Path: filepath.Join(stateDir, "journal.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var revoked int
	if err := db.QueryRowContext(t.Context(), "SELECT revoked FROM authorization WHERE principal=?", "device").Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Fatalf("authorization revocation was not persisted: revoked=%d", revoked)
	}
}

func assertCoAPRevoked(t *testing.T, conn *coapClient.Conn, credentials *detachedCredentials, request core.Request) {
	t.Helper()
	frame := adapter.FrameRequest{Version: 1, CorrelationID: "coaps-revoked-" + request.RequestID, Profile: "encrypted", Envelope: credentials.token(t, request, true)}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	path := "/v1/coap/rpc"
	if request.Operation == core.Watch {
		frame.Stream = true
		body, err = json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		path = "/v1/coap/observe/register"
	}
	response, err := conn.Post(ctx, path, message.AppJSON, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	payload, readErr := response.ReadBody()
	code := response.Code()
	conn.ReleaseMessage(response)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if code != codes.Content && code != codes.Unauthorized && code != codes.BadRequest {
		t.Fatalf("revoked %s request code=%v payload=%q", request.Operation, code, payload)
	}
	if request.Operation != core.Watch {
		if code == codes.Content {
			t.Fatalf("revoked %s request was accepted: payload=%q", request.Operation, payload)
		}
		return
	}
	if code != codes.Content {
		return
	}
	var registration coapRegistration
	if err := json.Unmarshal(payload, &registration); err != nil || registration.ObservationToken == "" {
		t.Fatalf("revoked watch returned malformed registration: code=%v payload=%q err=%v", code, payload, err)
	}
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	observeCtx, observeCancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer observeCancel()
	observe, err := conn.NewGetRequest(observeCtx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	observe.SetToken(token)
	observe.SetObserve(0)
	notifications := make(chan coapNotification, 8)
	watch, err := conn.DoObserve(observe, func(response *pool.Message) {
		body, readErr := response.ReadBody()
		if readErr != nil {
			return
		}
		format, _ := response.ContentFormat()
		observeValue, observeErr := response.Observe()
		select {
		case notifications <- coapNotification{body: append([]byte(nil), body...), format: format, observe: observeValue, hasObserve: observeErr == nil}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("revoked watch carrier failed before its authorization result: %v", err)
	}
	defer cancelCoAPSObservation(watch)
	select {
	case ready := <-notifications:
		var marker struct {
			Event string `json:"event"`
		}
		if ready.format != message.AppJSON || json.Unmarshal(ready.body, &marker) != nil || marker.Event != "ready" {
			t.Fatalf("revoked watch ready marker=%q format=%v", ready.body, ready.format)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("revoked watch did not return ready marker")
	}
	frameResponse := coapObserveFrame(t, notifications, time.After(4*time.Second))
	if frameResponse.Event != "error" || frameResponse.Error == nil || frameResponse.Error.Code != core.Forbidden {
		t.Fatalf("revoked watch activation response=%+v", frameResponse)
	}
}
