// airc-load exercises an isolated AIRC server with scheduled synthetic clients.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	DisconnectPercent int           `json:"disconnect_percent"`
	DisconnectAfter   time.Duration `json:"disconnect_after_ns"`
	Agents            string        `json:"agents"`
	Rooms             int           `json:"rooms"`
	Duration          time.Duration `json:"duration_ns"`
	Warmup            time.Duration `json:"warmup_ns"`
	Timeout           time.Duration `json:"timeout_ns"`
	Rate              float64       `json:"operations_per_agent_second"`
	BodyBytes         int           `json:"body_bytes"`
	Persist           bool          `json:"persistent_history"`
	Out               string        `json:"-"`
}

func (c config) counts() ([]int, error) {
	var counts []int
	seen := map[int]bool{}
	for part := range strings.SplitSeq(c.Agents, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 1000 || seen[n] {
			return nil, errors.New("agents must be distinct counts between 1 and 1000")
		}
		counts = append(counts, n)
		seen[n] = true
	}
	if c.Rooms < 1 || c.Rooms > 64 || c.Duration < time.Second || c.Duration > 10*time.Minute || c.Warmup < 0 || c.Warmup > time.Minute || c.Timeout < time.Millisecond || c.Timeout > time.Minute || !(c.Rate >= 0.1 && c.Rate <= 100) || c.BodyBytes < 64 || c.BodyBytes > 3500 {
		return nil, errors.New("invalid workload: rooms 1-64, duration 1s-10m, warmup 0-1m, timeout 1ms-1m, rate 0.1-100, body-bytes 64-3500")
	}
	for _, n := range counts {
		if int(c.Duration.Seconds()*c.Rate) < 1 || float64(n)*(c.Duration+c.Warmup).Seconds()*c.Rate > 1_000_000 {
			return nil, errors.New("workload needs at least one slot per agent and at most one million total warmup/measurement slots per case")
		}
	}
	if c.DisconnectPercent < 0 || c.DisconnectPercent > 100 || c.DisconnectAfter < 0 || c.DisconnectPercent > 0 && c.DisconnectAfter >= c.Duration {
		return nil, errors.New("disconnect-percent must be 0-100 and disconnect-after must be before measurement end (0 means midpoint)")
	}
	return counts, nil
}

func main() {
	if err := mainErr(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func mainErr() error {
	c := config{}
	flag.IntVar(&c.DisconnectPercent, "disconnect-percent", 0, "reset this percentage of server-side sockets during measurement (fault scenario)")
	flag.DurationVar(&c.DisconnectAfter, "disconnect-after", 0, "time after measurement start to reset sockets; 0 means midpoint")
	flag.StringVar(&c.Agents, "agents", "50,100,500,1000", "comma-separated concurrent client counts")
	flag.IntVar(&c.Rooms, "rooms", 10, "rooms; each client subscribes to one plus its mention inbox")
	flag.DurationVar(&c.Duration, "duration", 30*time.Second, "scheduled measurement duration per case")
	flag.DurationVar(&c.Warmup, "warmup", 5*time.Second, "same-workload warmup, excluded from latency results")
	flag.DurationVar(&c.Timeout, "timeout", 5*time.Second, "deadline per operation")
	flag.Float64Var(&c.Rate, "rate", 1, "scheduled operations per second per agent")
	flag.IntVar(&c.BodyBytes, "body-bytes", 256, "post and mention body size")
	flag.BoolVar(&c.Persist, "persist", true, "sync history to temporary disk storage")
	flag.StringVar(&c.Out, "out", "", "new output directory (default timestamped directory)")
	serve := flag.Bool("serve", false, "internal isolated server child mode")
	flag.Parse()
	counts, err := c.counts()
	if err != nil {
		return err
	}
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *serve {
		return serveChild(ctx, c, counts[0])
	}
	if c.Out == "" {
		c.Out = "airc-load-" + time.Now().Format("20060102-150405")
	}
	if err := os.Mkdir(c.Out, 0700); err != nil {
		return fmt.Errorf("create new output directory: %w", err)
	}
	reportFile, err := os.Create(filepath.Join(c.Out, "results.jsonl"))
	if err != nil {
		return err
	}
	defer reportFile.Close()
	failed := false
	for _, n := range counts {
		fmt.Fprintf(os.Stderr, "Running %d agents, %.0f scheduled ops/s, %s warmup + %s measurement\n", n, float64(n)*c.Rate, c.Warmup, c.Duration)
		r, runErr := runCase(ctx, c, n)
		if runErr != nil {
			r.Error = runErr.Error()
			failed = true
		}
		if err := json.NewEncoder(reportFile).Encode(r); err != nil {
			return err
		}
		if err := reportFile.Sync(); err != nil {
			return err
		}
		if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if failed {
		return errors.New("one or more load cases failed; inspect results and raw samples")
	}
	return nil
}
