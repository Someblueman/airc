//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package service

import (
	"context"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"os"
	"testing"
	"time"
)

func mustSystemVKey(t *testing.T, value string) int32 {
	t.Helper()
	key, err := config.SystemVQueueKey(value)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSystemVQueueOwnerMissingResponsePreservesIngressAbsence(t *testing.T) {
	c, journal, l, _ := systemVPacketFixture(t)
	owner, err := newSystemVQueueOwner(t.Context(), c, l, journal, &dispatch.Dispatcher{}, make(chan struct{}, 1), make(chan struct{}, 1))
	if err == nil || owner != nil {
		t.Fatal("missing response accepted", err)
	}
	if q, err := sysvqueue.Open(mustSystemVKey(t, l.Address)); !errors.Is(err, os.ErrNotExist) {
		if q != nil {
			_ = q.Close()
		}
		t.Fatal("ingress created before response verification", err)
	}
	if _, err := journal.LoadSystemVQueueOwnership(t.Context(), l.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed startup registered ingress", err)
	}
}

func TestSystemVQueueOwnerRoutesAndJoinedClose(t *testing.T) {
	c, journal, l, name := systemVPacketFixture(t)
	response, err := sysvqueue.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Close()
	owner, err := newSystemVQueueOwner(t.Context(), c, l, journal, &dispatch.Dispatcher{}, make(chan struct{}, 1), make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	ingress, err := sysvqueue.Open(mustSystemVKey(t, l.Address))
	if err != nil {
		t.Fatal(err)
	}
	defer ingress.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := ingress.WriteMessage(ctx, []byte("whole ingress fragment")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, sysvqueue.MessageBytes)
	n, addr, err := owner.conn.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "whole ingress fragment" || addr.String() != l.Address {
		t.Fatal("ingress packet mapping", n, addr, err)
	}
	if _, err := owner.conn.WriteTo([]byte("whole response fragment"), &adapter.SystemVQueueAddr{Key: name}); err != nil {
		t.Fatal(err)
	}
	n, err = response.ReadMessage(ctx, buf)
	if err != nil || string(buf[:n]) != "whole response fragment" {
		t.Fatal("fixed response mapping", n, err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := owner.conn.ReadFrom(buf); done <- err }()
	if err := owner.close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed read succeeded")
		}
	case <-ctx.Done():
		t.Fatal("read did not join close")
	}
	if q, err := sysvqueue.Open(mustSystemVKey(t, l.Address)); !errors.Is(err, os.ErrNotExist) {
		if q != nil {
			_ = q.Close()
		}
		t.Fatal("owned ingress survived close", err)
	}
	opened, err := sysvqueue.Open(name)
	if err != nil {
		t.Fatal("external response removed", err)
	}
	defer opened.Close()
	if opened.Identity() != response.Identity() {
		t.Fatal("external response replaced")
	}
}
