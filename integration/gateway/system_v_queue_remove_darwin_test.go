//go:build darwin && arm64

package gateway_test

import (
	"os/exec"
	"strconv"
	"testing"
)

func removeSystemVQueue(t *testing.T, id int) {
	t.Helper()
	output, err := exec.Command("/usr/bin/ipcrm", "-q", strconv.Itoa(id)).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
}
