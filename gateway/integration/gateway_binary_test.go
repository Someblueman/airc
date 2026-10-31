package gateway_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/testfs"
)

// The current gateway executable is immutable within a test process. Each
// invocation still owns its configuration, credentials, journal and lifetime.
var currentGatewayFixture struct {
	once   sync.Once
	root   string
	binary string
	err    error
}

func currentGatewayBinary(t *testing.T) string {
	t.Helper()
	currentGatewayFixture.once.Do(func() {
		currentGatewayFixture.root, currentGatewayFixture.binary, currentGatewayFixture.err = buildCurrentGateway()
	})
	if currentGatewayFixture.err != nil {
		t.Fatal(currentGatewayFixture.err)
	}
	if shortTimersFor(t) {
		return shortTimerGatewayBinary(t)
	}
	return currentGatewayFixture.binary
}

func buildCurrentGateway() (string, string, error) {
	root, err := testfs.MkdirTemp("agw-current-")
	if err != nil {
		return "", "", err
	}
	binary := filepath.Join(root, "airc-gateway")
	if err := buildGateway(binary, ""); err != nil {
		_ = os.RemoveAll(root)
		return "", "", err
	}
	return root, binary, nil
}

// gatewayBuildTimeout bounds one build of the gateway. A race-enabled build
// with an empty build cache takes about four minutes on a 4-vCPU host, and a
// failed build fails every test in the package, so the bound only catches a
// hung toolchain.
const gatewayBuildTimeout = 10 * time.Minute

func buildGateway(binary, ldflags string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gatewayBuildTimeout)
	defer cancel()
	args := []string{"build", fmt.Sprintf("-race=%t", gatewayTestRace), "-trimpath", "-ldflags=" + ldflags, "-o", binary, "./cmd/airc-gateway"}
	build := exec.CommandContext(ctx, "go", args...)
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building current gateway: %w\n%s", err, output)
	}
	return os.Chmod(binary, 0500)
}

// gatewayTimer is a production lease or idle bound that a test may shorten,
// so that it waits out the same expiry path in seconds. The short values of
// every timer are linked into one short-timer gateway build (see
// core.LinkedDuration), so one link serves every test that waits out a
// timer; where the timer is exported, a test can also set it for gateway
// parts running in the test process (shortInProcessTimer).
type gatewayTimer struct {
	symbol         string
	short, release time.Duration
	inProcess      *time.Duration
}

var (
	// The short read lease outlives the HTTP server's five-second write
	// bound, which a quiet SSE stream must reach the lease's end under.
	readLeaseTimer     = gatewayTimer{"github.com/Someblueman/airc/gateway/internal/store.linkedReadLease", 8 * time.Second, 15 * time.Second, &store.ReadLeaseDuration}
	websocketPingTimer = gatewayTimer{"github.com/Someblueman/airc/gateway/internal/adapter.linkedWebSocketPing", 2 * time.Second, 10 * time.Second, nil}
	shortGatewayTimers = []gatewayTimer{readLeaseTimer, websocketPingTimer}
)

// shortTimers names the tests whose gateways run the short-timer build, and
// holds that build, linked once.
var shortTimers struct {
	sync.Mutex
	tests map[string]bool
	build struct {
		once   sync.Once
		binary string
		err    error
	}
}

// shortGatewayTimer makes every gateway process that t (and its subtests)
// starts run the short-timer build, and returns timer's short value. Gateway
// parts running in the test process keep the release value: a test that
// runs them calls shortInProcessTimer.
func shortGatewayTimer(t *testing.T, timer gatewayTimer) time.Duration {
	t.Helper()
	shortTimers.Lock()
	defer shortTimers.Unlock()
	if shortTimers.tests == nil {
		shortTimers.tests = make(map[string]bool)
	}
	if name := t.Name(); !shortTimers.tests[name] {
		shortTimers.tests[name] = true
		t.Cleanup(func() {
			shortTimers.Lock()
			delete(shortTimers.tests, name)
			shortTimers.Unlock()
		})
	}
	return timer.short
}

// shortInProcessTimer is shortGatewayTimer for a test whose gateway parts
// run in the test process: it also sets the package variable they read,
// which every in-process gateway in the package shares, so the test must
// not run in parallel.
func shortInProcessTimer(t *testing.T, timer gatewayTimer) time.Duration {
	t.Helper()
	if timer.inProcess == nil {
		t.Fatalf("%s is not set in process", timer.symbol)
	}
	released := *timer.inProcess
	*timer.inProcess = timer.short
	t.Cleanup(func() { *timer.inProcess = released })
	return shortGatewayTimer(t, timer)
}

// shortTimersFor reports whether t or a test enclosing it asked for the
// short-timer build.
func shortTimersFor(t *testing.T) bool {
	shortTimers.Lock()
	defer shortTimers.Unlock()
	for name := t.Name(); ; name = name[:strings.LastIndexByte(name, '/')] {
		if shortTimers.tests[name] {
			return true
		}
		if !strings.Contains(name, "/") {
			return false
		}
	}
}

// shortTimerLDFlags links every timer's short value.
func shortTimerLDFlags() string {
	var flags []string
	for _, timer := range shortGatewayTimers {
		flags = append(flags, fmt.Sprintf("-X=%s=%s", timer.symbol, timer.short))
	}
	return strings.Join(flags, " ")
}

// The short-timer build lives inside the current gateway's root, which
// TestMain removes, and reuses its build cache, so it costs only a link.
func shortTimerGatewayBinary(t *testing.T) string {
	t.Helper()
	build := &shortTimers.build
	build.once.Do(func() {
		build.binary = filepath.Join(currentGatewayFixture.root, "airc-gateway-short")
		ldflags := shortTimerLDFlags()
		if build.err = buildGateway(build.binary, ldflags); build.err == nil {
			build.err = linkedSymbolsExist(build.binary, ldflags)
		}
	})
	if build.err != nil {
		t.Fatal(build.err)
	}
	return build.binary
}

// The linker ignores -X for a symbol that does not exist, so a renamed
// variable would silently keep its release value. Refuse that build instead.
func linkedSymbolsExist(binary, ldflags string) error {
	output, err := exec.Command("go", "tool", "nm", binary).Output()
	if err != nil {
		return fmt.Errorf("listing gateway symbols: %w", err)
	}
	symbols := make(map[string]bool)
	for line := range strings.Lines(string(output)) {
		if fields := strings.Fields(line); len(fields) > 0 {
			symbols[fields[len(fields)-1]] = true
		}
	}
	for flag := range strings.FieldsSeq(ldflags) {
		symbol, _, _ := strings.Cut(strings.TrimPrefix(flag, "-X="), "=")
		if !symbols[symbol] {
			return fmt.Errorf("gateway has no linkable variable %s", symbol)
		}
	}
	return nil
}
