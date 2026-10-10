//go:build linux && (arm64 || amd64)

package gateway_test

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func systemVQueuedBytes(t *testing.T, id int) int {
	t.Helper()
	data, err := os.ReadFile("/proc/sysvipc/msg")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	header := strings.Fields(lines[0])
	idColumn, bytesColumn := -1, -1
	for i, name := range header {
		if name == "msqid" {
			idColumn = i
		}
		if name == "cbytes" {
			bytesColumn = i
		}
	}
	if idColumn < 0 || bytesColumn < 0 {
		t.Fatal("missing kernel queue accounting columns")
	}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) != len(header) {
			t.Fatal("invalid kernel queue accounting row")
		}
		if fields[idColumn] == strconv.Itoa(id) {
			n, err := strconv.Atoi(fields[bytesColumn])
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatal("kernel queue accounting missing identifier", id)
	return 0
}
