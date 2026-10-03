package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func runCase(ctx context.Context, c config, n int) (r report, resultErr error) {
	r = report{Version: 1, Workload: "mixed-chat-v1", At: time.Now().UTC(), Config: c, Agents: n, Build: buildInfo(), GeneratorStart: resources()}
	dir, err := os.MkdirTemp("", "airc-load-server-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(dir)
	child, err := startChild(ctx, c, n, dir)
	if err != nil {
		return r, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, child.close())
		r.Server = child.samples
		r.Generator = resources()
	}()
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	commands := make([]chan phase, n)
	results := make(chan agentResult, n)
	for i := 0; i < n; i++ {
		commands[i] = make(chan phase, 1)
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			runAgent(ctx, c, child.address, index, n, commands[index], results)
		}(i)
	}
	var setup []sample
	for range n {
		select {
		case a := <-results:
			setup = append(setup, a.samples...)
			if a.connected {
				r.Connected++
			}
			if a.ready {
				r.Ready++
			}
		case <-ctx.Done():
			return r, ctx.Err()
		}
	}
	r.Setup = summarize(setup)
	if err := writeSamples(filepath.Join(c.Out, fmt.Sprintf("%d-setup.csv", n)), setup); err != nil {
		return r, err
	}
	if r.Ready != n {
		resultErr = fmt.Errorf("only %d/%d agents ready", r.Ready, n)
	}
	for _, p := range []phase{{duration: c.Warmup}, {duration: c.Duration, measured: true}} {
		if p.duration == 0 {
			continue
		}
		p.start = time.Now().Add(100 * time.Millisecond)
		for _, command := range commands {
			command <- p
		}
		var samples []sample
		for range n {
			select {
			case a := <-results:
				samples = append(samples, a.samples...)
				if p.measured {
					r.LiveEvents += a.live
					r.Mentions += a.mentions
					r.Gaps += a.gaps
				}
			case <-ctx.Done():
				return r, errors.Join(resultErr, ctx.Err())
			}
		}
		metrics := summarize(samples)
		if p.measured {
			r.MeasurementStart = p.start.UTC()
			r.Metrics = metrics
			r.Elapsed = time.Since(p.start).Seconds()
			for _, m := range metrics {
				if m.Succeeded != m.Scheduled {
					resultErr = errors.Join(resultErr, errors.New("incomplete scheduled workload"))
					break
				}
			}
			// Sum independently of whether a failure was seen.
			total := 0
			for _, m := range metrics {
				total += m.Succeeded
			}
			r.Throughput = float64(total) / r.Elapsed
			if err := writeSamples(filepath.Join(c.Out, fmt.Sprintf("%d-samples.csv", n)), samples); err != nil {
				return r, errors.Join(resultErr, err)
			}
		} else {
			r.Warmup = metrics
		}
	}
	return r, resultErr
}
