//go:build darwin || linux

package service

import (
	"bytes"
	"os"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestRedisStartupFailureNeverClaimsReadyOrRetainsJournal(t *testing.T) {
	for _, mode := range []string{"pubsub", "streams"} {
		for _, kind := range []string{"password", "trust"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				c, cert := redisServiceConfig(t)
				var path string
				if mode == "pubsub" {
					c.RedisPubSub = []config.RedisPubSub{redisServicePubSub(t, c, cert, "127.0.0.1:6379")}
					path = c.RedisPubSub[0].Subscriber.PasswordFile
				} else {
					c.RedisStreams = []config.RedisStreams{redisServiceStreams(t, c, cert, "127.0.0.1:6379")}
					path = c.RedisStreams[0].Credential.PasswordFile
				}
				content := []byte{}
				if kind == "trust" {
					path, content = cert.CAPath, []byte("invalid CA")
				}
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				if err := c.CheckFiles(); err != nil {
					t.Fatal("fixture file references", err)
				}
				if err := Run(t.Context(), c, &output); err == nil || output.Len() != 0 {
					t.Fatal("failed preparation claimed ready", err)
				}
				journal, err := store.Open(t.Context(), c)
				if err != nil {
					t.Fatal("failed startup retained journal", err)
				}
				_ = journal.Close()
			})
		}
	}
}
