//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package service

import (
	"strconv"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func systemVPacketFixture(t *testing.T) (config.Config, *store.Store, config.Listener, int32) {
	t.Helper()
	c, journal, key := systemVIngressFixture(t)
	l := config.Listener{ID: "sysv", Protocol: "system_v_queue", Address: strconv.FormatInt(int64(key), 10)}
	_, other, response := systemVIngressFixture(t)
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if response == key {
		t.Fatal("fixture queue-key collision")
	}
	l.ReplyTo = strconv.FormatInt(int64(response), 10)
	return c, journal, l, response
}
