//go:build darwin && arm64

package gateway_test

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func systemVQueuedBytes(t *testing.T, id int) int {
	t.Helper()
	data, err := exec.Command("/usr/bin/ipcs", "-q", "-o").CombinedOutput()
	if err != nil || strings.Contains(string(data), "sysctlbyname") {
		t.Fatal("native queue accounting unavailable", err, string(data))
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 8 && fields[0] == "q" && fields[1] == strconv.Itoa(id) {
			n, err := strconv.Atoi(fields[6])
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatal("native queue accounting missing identifier", id)
	return 0
}
