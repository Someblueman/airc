package main

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestPercentilesAndFailures(t *testing.T) {
	var samples []sample
	for i := 100; i >= 1; i-- {
		samples = append(samples, sample{Operation: "post", Outcome: "ok", Started: 2 * time.Millisecond, Elapsed: time.Duration(i) * time.Millisecond})
	}
	for _, failure := range []string{"timeout", "rejected_430", "missed_slot", "unavailable", "cancelled_before_start"} {
		samples = append(samples, sample{Operation: "post", Outcome: failure, Elapsed: time.Hour})
	}
	m := summarize(samples)["post"]
	if m.Scheduled != 105 || m.Attempted != 102 || m.Succeeded != 100 {
		t.Fatalf("counts: %+v", m)
	}
	if *m.LatencyMS != (percentiles{50, 75, 90, 99, 100}) {
		t.Fatalf("nearest rank: %+v", m.LatencyMS)
	}
	if m.ScheduledLatencyMS.P99 != 101 {
		t.Fatalf("schedule latency: %+v", m.ScheduledLatencyMS)
	}
	if summarize([]sample{{Operation: "check", Outcome: "timeout"}})["check"].LatencyMS != nil {
		t.Fatal("no successes must yield null percentiles")
	}
}

func TestMissedScheduleDoesNotBecomeFastSuccess(t *testing.T) {
	a := agent{total: 1, alive: true, client: new(irc.Client), cfg: config{Rate: 10}}
	r := a.runPhase(context.Background(), phase{start: time.Now().Add(-time.Minute), duration: time.Second})
	if len(r.samples) != 10 {
		t.Fatalf("scheduled %d", len(r.samples))
	}
	for _, s := range r.samples {
		if s.Outcome != "missed_slot" {
			t.Fatalf("unexpected sample %+v", s)
		}
	}
}

func TestWorkloadBounds(t *testing.T) {
	base := config{Agents: "50,100,500,1000", Rooms: 10, Duration: 30 * time.Second, Warmup: time.Second, Timeout: time.Second, Rate: 1, BodyBytes: 256}
	if _, err := base.counts(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*config){
		func(c *config) { c.Agents = "1001" }, func(c *config) { c.Agents = "50,50" },
		func(c *config) { c.Rate = math.NaN() }, func(c *config) { c.Rate = math.Inf(1) },
		func(c *config) { c.Rate = 100; c.Duration = 10 * time.Minute }, func(c *config) { c.BodyBytes = 63 },
		func(c *config) { c.DisconnectPercent = 101 }, func(c *config) { c.DisconnectPercent = 10; c.DisconnectAfter = c.Duration },
	} {
		c := base
		mutate(&c)
		if _, err := c.counts(); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}
