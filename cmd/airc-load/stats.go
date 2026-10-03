package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"time"
)

type sample struct {
	Agent     int
	Operation string
	Slot      int
	Scheduled time.Duration
	Started   time.Duration
	Elapsed   time.Duration
	Outcome   string
}

type percentiles struct {
	P50 float64 `json:"p50"`
	P75 float64 `json:"p75"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

type metric struct {
	Scheduled          int            `json:"scheduled"`
	Attempted          int            `json:"attempted"`
	Succeeded          int            `json:"succeeded"`
	Outcomes           map[string]int `json:"outcomes"`
	LatencyMS          *percentiles   `json:"successful_operation_latency_ms"`
	ScheduledLatencyMS *percentiles   `json:"successful_scheduled_to_completion_ms"`
}

type report struct {
	Version          int               `json:"schema_version"`
	Workload         string            `json:"workload"`
	At               time.Time         `json:"at"`
	Config           config            `json:"config"`
	Agents           int               `json:"agents"`
	Connected        int               `json:"connected"`
	Ready            int               `json:"ready"`
	Build            map[string]string `json:"build"`
	Setup            map[string]metric `json:"setup"`
	Warmup           map[string]metric `json:"warmup"`
	Metrics          map[string]metric `json:"operations"`
	Elapsed          float64           `json:"measurement_and_drain_seconds"`
	Throughput       float64           `json:"successful_operations_per_second"`
	LiveEvents       uint64            `json:"live_events_received"`
	Mentions         uint64            `json:"mention_events_received"`
	Gaps             uint64            `json:"check_gap_markers"`
	Server           []resource        `json:"server_resources"`
	Generator        resource          `json:"generator_final_resources"`
	GeneratorStart   resource          `json:"generator_initial_resources"`
	MeasurementStart time.Time         `json:"measurement_start"`
	Error            string            `json:"error,omitempty"`
}

func quantiles(values []time.Duration) *percentiles {
	if len(values) == 0 {
		return nil
	}
	slices.Sort(values)
	q := func(p float64) float64 {
		return float64(values[int(math.Ceil(p*float64(len(values))))-1]) / float64(time.Millisecond)
	}
	return &percentiles{q(.50), q(.75), q(.90), q(.99), q(1)}
}

func summarize(samples []sample) map[string]metric {
	out := map[string]metric{}
	latencies, scheduled := map[string][]time.Duration{}, map[string][]time.Duration{}
	for _, s := range samples {
		m := out[s.Operation]
		if m.Outcomes == nil {
			m.Outcomes = map[string]int{}
		}
		m.Scheduled++
		m.Outcomes[s.Outcome]++
		if s.Outcome != "missed_slot" && s.Outcome != "unavailable" && s.Outcome != "cancelled_before_start" {
			m.Attempted++
		}
		if s.Outcome == "ok" {
			m.Succeeded++
			latencies[s.Operation] = append(latencies[s.Operation], s.Elapsed)
			scheduled[s.Operation] = append(scheduled[s.Operation], s.Started-s.Scheduled+s.Elapsed)
		}
		out[s.Operation] = m
	}
	for op, m := range out {
		m.LatencyMS = quantiles(latencies[op])
		m.ScheduledLatencyMS = quantiles(scheduled[op])
		out[op] = m
	}
	return out
}

func writeSamples(path string, samples []sample) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.Write([]string{"agent", "operation", "slot", "scheduled_ns", "started_ns", "elapsed_ns", "outcome"}); err != nil {
		_ = f.Close()
		return err
	}
	for _, s := range samples {
		if err := w.Write([]string{strconv.Itoa(s.Agent), s.Operation, strconv.Itoa(s.Slot), fmt.Sprint(int64(s.Scheduled)), fmt.Sprint(int64(s.Started)), fmt.Sprint(int64(s.Elapsed)), s.Outcome}); err != nil {
			_ = f.Close()
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func buildInfo() map[string]string {
	m := map[string]string{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": strconv.Itoa(runtime.GOMAXPROCS(0)), "cpus": strconv.Itoa(runtime.NumCPU())}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" || s.Key == "vcs.modified" {
				m[s.Key] = s.Value
			}
		}
	}
	return m
}
