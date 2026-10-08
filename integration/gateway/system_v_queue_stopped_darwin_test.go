//go:build darwin && arm64

package gateway_test

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func waitSystemVGatewayStopped(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		output, err := exec.Command("/bin/ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(strings.TrimSpace(string(output)), "T") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("gateway did not enter kernel stopped state")
}
