//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestGatewayServeCLIReadOpaqueCursorScopesAndRestart(t *testing.T) {
	for _, op := range []core.Operation{core.HistoryRead, core.Search, core.Watch} {
		t.Run(string(op), func(t *testing.T) { cliReadCursorRestart(t, op) })
	}
}
