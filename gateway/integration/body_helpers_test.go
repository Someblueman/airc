//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"testing"
)

func mustBody(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
