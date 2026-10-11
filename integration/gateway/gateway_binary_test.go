package gateway_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testfs"
)

// The current gateway executable is immutable within a test process. Each
// invocation still owns its configuration, credentials, journal and lifetime.
var currentGatewayFixture struct {
	once   sync.Once
	root   string
	binary string
	digest string
	err    error
}

func currentGatewayBinary(t *testing.T) string {
	t.Helper()
	currentGatewayFixture.once.Do(func() {
		currentGatewayFixture.root, currentGatewayFixture.binary, currentGatewayFixture.digest, currentGatewayFixture.err = buildCurrentGateway()
	})
	if currentGatewayFixture.err != nil {
		t.Fatal(currentGatewayFixture.err)
	}
	if ldflags := shortTimerFlags(t); ldflags != "" {
		return shortTimerGatewayBinary(t, ldflags)
	}
	t.Logf("current gateway sha256=%s", currentGatewayFixture.digest)
	return currentGatewayFixture.binary
}

func buildCurrentGateway() (string, string, string, error) {
	if root, binary, digest, err := prebuiltFixtureBinary("AGW_GATEWAY_BINARY", "AGW_GATEWAY_SHA256", "github.com/Someblueman/airc/cmd/airc-gateway", gatewayTestRace); err != nil || binary != "" {
		return root, binary, digest, err
	}
	root, err := testfs.MkdirTemp("agw-current-")
	if err != nil {
		return "", "", "", err
	}
	binary := filepath.Join(root, "airc-gateway")
	digest, err := buildGateway(binary, "")
	if err != nil {
		_ = os.RemoveAll(root)
		return "", "", "", err
	}
	return root, binary, digest, nil
}

func buildGateway(binary, ldflags string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := []string{"build", fmt.Sprintf("-race=%t", gatewayTestRace), "-trimpath", "-ldflags=" + ldflags, "-o", binary, "./cmd/airc-gateway"}
	build := exec.CommandContext(ctx, "go", args...)
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building current gateway: %w\n%s", err, output)
	}
	if err := os.Chmod(binary, 0500); err != nil {
		return "", err
	}
	f, err := os.Open(binary)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	_, readErr := io.Copy(digest, f)
	if err := errors.Join(readErr, f.Close()); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

// gatewayTimer is a production lease or idle bound that a test may shorten,
// so that it waits out the same expiry path in seconds. The short value is
// linked into a separate gateway build (see core.LinkedDuration) and, where
// the timer is exported, also set for gateway parts running in the test.
type gatewayTimer struct {
	symbol         string
	short, release time.Duration
	inProcess      *time.Duration
}

var (
	readLeaseTimer      = gatewayTimer{"github.com/Someblueman/airc/internal/gateway/store.linkedReadLease", 6 * time.Second, 15 * time.Second, &store.ReadLeaseDuration}
	dtlsPeerIdleTimer   = gatewayTimer{"github.com/Someblueman/airc/internal/gateway/service.linkedDTLSPeerIdle", 8 * time.Second, 30 * time.Second, nil}
	webRTCPeerIdleTimer = gatewayTimer{"github.com/Someblueman/airc/internal/gateway/adapter.linkedWebRTCPeerIdle", 8 * time.Second, 30 * time.Second, nil}
	websocketPingTimer  = gatewayTimer{"github.com/Someblueman/airc/internal/gateway/adapter.linkedWebSocketPing", 2 * time.Second, 10 * time.Second, nil}
)

var shortTimers struct {
	sync.Mutex
	flags  map[string][]string // test name -> -X flags
	builds map[string]*shortTimerBuild
}

type shortTimerBuild struct {
	once           sync.Once
	binary, digest string
	err            error
}

// shortGatewayTimer makes every gateway that t (and its subtests) starts link
// the short value of timer, and returns the value in force. A supplied
// prebuilt gateway cannot be relinked, so it keeps the release value.
func shortGatewayTimer(t *testing.T, timer gatewayTimer) time.Duration {
	t.Helper()
	if _, supplied := os.LookupEnv("AGW_GATEWAY_BINARY"); supplied {
		return timer.release
	}
	shortTimers.Lock()
	defer shortTimers.Unlock()
	if shortTimers.flags == nil {
		shortTimers.flags = make(map[string][]string)
	}
	name := t.Name()
	if _, ok := shortTimers.flags[name]; !ok {
		t.Cleanup(func() {
			shortTimers.Lock()
			delete(shortTimers.flags, name)
			shortTimers.Unlock()
		})
	}
	shortTimers.flags[name] = append(shortTimers.flags[name], fmt.Sprintf("-X=%s=%s", timer.symbol, timer.short))
	if timer.inProcess != nil {
		released := *timer.inProcess
		*timer.inProcess = timer.short
		t.Cleanup(func() { *timer.inProcess = released })
	}
	return timer.short
}

func shortTimerFlags(t *testing.T) string {
	shortTimers.Lock()
	defer shortTimers.Unlock()
	var flags []string
	for name := t.Name(); ; name = name[:strings.LastIndexByte(name, '/')] {
		flags = append(flags, shortTimers.flags[name]...)
		if !strings.Contains(name, "/") {
			break
		}
	}
	slices.Sort(flags)
	return strings.Join(slices.Compact(flags), " ")
}

// Short-timer builds live inside the current gateway's root, which TestMain
// removes, and reuse its build cache, so each costs only a link.
func shortTimerGatewayBinary(t *testing.T, ldflags string) string {
	t.Helper()
	shortTimers.Lock()
	if shortTimers.builds == nil {
		shortTimers.builds = make(map[string]*shortTimerBuild)
	}
	build := shortTimers.builds[ldflags]
	if build == nil {
		build = &shortTimerBuild{binary: filepath.Join(currentGatewayFixture.root, fmt.Sprintf("airc-gateway-%d", len(shortTimers.builds)))}
		shortTimers.builds[ldflags] = build
	}
	shortTimers.Unlock()
	build.once.Do(func() { build.digest, build.err = buildGateway(build.binary, ldflags) })
	if build.err != nil {
		t.Fatal(build.err)
	}
	t.Logf("short-timer gateway ldflags=%q sha256=%s", ldflags, build.digest)
	return build.binary
}
