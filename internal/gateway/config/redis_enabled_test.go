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
	c.Limits.SessionsPerAuthor = 1
	c.Limits.Watches, c.Limits.WatchesPerAuthor = 0, 0
	if err := c.Validate(); err != nil {
		t.Fatal("enabled broker", err)
	}
}

func TestRedisStreamsCombinedBrokerCapacity(t *testing.T) {
	c := streamsConfig()
	c.RedisStreams[0].Enabled = true
	c.RedisStreams[0].Route.Operations = c.RedisStreams[0].Route.Operations[:1]
	pubsub := redisConfig().RedisPubSub[0]
	pubsub.Enabled = true
	c.RedisPubSub = []RedisPubSub{pubsub}
	c.Limits.Workers, c.Limits.SessionsPerAuthor = 1, 1
	c.Limits.Watches, c.Limits.WatchesPerAuthor = 0, 0
	for _, sessions := range []int{2, 3} {
		c.Limits.Sessions = sessions
		if err := c.Validate(); (err == nil) != (sessions == 3) {
			t.Fatalf("combined capacity %d: %v", sessions, err)
		}
	}
	c.RedisPubSub = nil
	c.Limits.Sessions = 1
	if err := c.Validate(); err != nil {
		t.Fatal("single Streams socket", err)
	}
	second := c.RedisStreams[0]
	second.ID = "second"
	second.Route.Stream, second.Route.ResultStream = "second-events", "second-results"
	c.RedisStreams = append(c.RedisStreams, second)
	if err := c.Validate(); err == nil {
		t.Fatal("Streams sockets exceeded pool")
	}
}
