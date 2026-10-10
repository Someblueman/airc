package config

import (
	"fmt"
	"testing"
)

func TestRedisBrokerCountAndChannelRoles(t *testing.T) {
	for _, count := range []int{32, 33} {
		c := redisConfig()
		b := c.RedisPubSub[0]
		c.RedisPubSub = nil
		for i := range count {
			b.ID, b.Address = fmt.Sprintf("broker%d", i), fmt.Sprintf("127.0.0.1:%d", 6379+i)
			c.RedisPubSub = append(c.RedisPubSub, b)
		}
		if err := c.Validate(); (err == nil) != (count == 32) {
			t.Fatalf("brokers=%d: %v", count, err)
		}
	}
	c := redisConfig()
	r := c.RedisPubSub[0].Routes[0]
	r.RequestChannel, r.ResponseChannel = r.ResponseChannel, "second response"
	c.RedisPubSub[0].Routes = append(c.RedisPubSub[0].Routes, r)
	if err := c.Validate(); err == nil {
		t.Fatal("cross-role collision")
	}
	c = redisConfig()
	c.RedisPubSub[0].Routes[0].RequestChannel = ""
	if err := c.Validate(); err == nil {
		t.Fatal("empty channel")
	}
}
