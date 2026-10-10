package config

import "testing"

func TestRedisStreamsCanonicalEndpointNamespace(t *testing.T) {
	c := streamsConfig()
	c.RedisStreams[0].Address = "[::1]:6379"
	b := c.RedisStreams[0]
	b.ID, b.Address, b.Namespace = "second", "[0:0:0:0:0:0:0:1]:06379", "other"
	b.Route.Stream, b.Route.ResultStream = "next", "next-result"
	c.RedisStreams = append(c.RedisStreams, b)
	if err := c.Validate(); err == nil {
		t.Fatal("equivalent endpoint minted a second namespace")
	}
	c.RedisStreams[1].Namespace = "broker"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.RedisStreams[1].Route.ResultStream = "events"
	if err := c.Validate(); err == nil {
		t.Fatal("result stream aliases request stream")
	}
}
