//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestGatewayServeCLIReadOpaqueCursorScopesAndRestart(t *testing.T) {
	t.Parallel()
	for _, op := range []core.Operation{core.HistoryRead, core.Search, core.Watch} {
		t.Run(string(op), func(t *testing.T) { cliReadCursorRestart(t, op) })
	}
}
