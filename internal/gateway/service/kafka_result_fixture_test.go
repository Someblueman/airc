//go:build darwin || linux

package service

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func verifyKafkaServiceResult(t *testing.T, c config.Config, pub ed25519.PublicKey, request core.Request, token []byte) {
	t.Helper()
	recordID, err := config.KafkaRecordID("history", "events", 2, 23)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("kafka-response\x00" + recordID + "\x00" + request.RequestID + "\x00results\x003\x00" + request.Target.Kind + "\x00" + request.Target.Name))
	scope := auth.ResponseScope{Realm: c.Realm, Principal: c.Principals[0].ID, Binding: c.Principals[0].Binding, Route: hex.EncodeToString(sum[:])}
	plain, err := auth.OpenSignedResponse(string(token), "response", pub, scope, time.Now())
	if err != nil {
		t.Fatal("signed response", err)
	}
	var result adapter.KafkaResult
	if err := json.Unmarshal(plain, &result); err != nil || result.RequestID != request.RequestID || result.RecordID != recordID || result.Offset != 23 || result.Result == nil || result.Result.Result == nil || !result.Result.Result.Durable || result.Result.Result.Accepted != nil {
		t.Fatal("whole correlated durable result", err)
	}
}
func verifyKafkaServiceJob(t *testing.T, c config.Config, journal *store.Store, request core.Request, payload string) {
	t.Helper()
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := v.AuthenticateSigned(payload, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	job, err := journal.Lookup(t.Context(), verified.Identity, request.RequestID)
	if err != nil || job.Request.Message != request.Message || job.Request.Target == nil || *job.Request.Target != *request.Target || job.Request.Operation != core.Send {
		t.Fatal("original intent changed", job, err)
	}
}
