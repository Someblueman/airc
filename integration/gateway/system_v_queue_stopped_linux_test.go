//go:build linux && arm64

package gateway_test

import "testing"

func waitSystemVGatewayStopped(t *testing.T, pid int) { t.Helper(); waitPOSIXGatewayStopped(t, pid) }
