package config

import "testing"

func TestRedisStreamRequestIdentity(t *testing.T) {
	id, err := RedisStreamRequestID("broker", "events", "1-0")
	if err != nil || id != "bc549bc91e88964ae21e0728f29da5e4f9da4b5631437db1d59b4520efc6746e" {
		t.Fatalf("fixed identity vector: %q, %v", id, err)
	}
	for _, v := range [][3]string{{"other", "events", "1-0"}, {"broker", "other", "1-0"}, {"broker", "events", "1-1"}, {"events", "broker", "1-0"}} {
		other, err := RedisStreamRequestID(v[0], v[1], v[2])
		if err != nil || other == id {
			t.Fatalf("unbound identity %v", v)
		}
	}
	for _, v := range [][3]string{{"", "events", "1-0"}, {"bad\x00", "events", "1-0"}, {"broker", "bad\x00", "1-0"}, {"broker", "events", "0-0"}, {"broker", "events", "01-0"}} {
		if value, err := RedisStreamRequestID(v[0], v[1], v[2]); err == nil || value != "" {
			t.Fatalf("invalid identity %v: %q", v, value)
		}
	}
	// Consumer/group, configured owner ID and dial address are deliberately absent
	// from this identity; they must not mint new journal requests after a restart.
	if same, err := RedisStreamRequestID("broker", "events", "1-0"); err != nil || same != id {
		t.Fatal("identity changed")
	}
}
