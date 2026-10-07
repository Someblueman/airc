//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func stompAdmissionRoute(f amqpAdmissionFixture) config.STOMPRoute {
	r := f.route()
	return config.STOMPRoute{Principal: r.Principal, RequestDestination: r.RequestAddress, ResponseDestination: r.ResponseAddress, Target: r.Target, Operations: r.Operations}
}

func stompAdmissionFrame(t *testing.T, f amqpAdmissionFixture, r core.Request, shift int64) stompwire.Frame {
	t.Helper()
	issued, expires := time.Now().Unix()+shift, time.Now().Unix()+shift+300
	if r.Operation == core.Send {
		if r.IssuedAt != nil && r.ExpiresAt != nil {
			issued, expires = *r.IssuedAt, *r.ExpiresAt
		} else {
			r.IssuedAt, r.ExpiresAt = &issued, &expires
		}
	}
	body, err := json.Marshal(auth.MessageEnvelope{Version: 1, Audience: f.c.Realm, IssuedAt: issued, ExpiresAt: expires, Request: r})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := f.signer.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return stompwire.Frame{Command: "SEND", Headers: []stompwire.Header{{Key: "destination", Value: "request"}, {Key: "content-type", Value: "application/jose"}, {Key: "content-length", Value: strconv.Itoa(len(token))}, {Key: "receipt", Value: "wire-receipt"}}, Body: []byte(token)}
}
