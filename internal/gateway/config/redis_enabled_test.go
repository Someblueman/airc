package config

import "testing"

func TestRedisEnabledBrokerCapacity(t *testing.T) {
	c := redisConfig()
	c.RedisPubSub[0].Enabled = true
	c.Limits.Sessions = 1
	if err := c.Validate(); err == nil {
		t.Fatal("enabled pair exceeds capacity")
	}
	c.Limits.Sessions, c.Limits.Workers = 2, 1
	c.Limits.SessionsPerPrincipal = 1
	c.Limits.Watches, c.Limits.WatchesPerPrincipal = 0, 0
	if err := c.Validate(); err != nil {
		t.Fatal("enabled broker", err)
	}
}
