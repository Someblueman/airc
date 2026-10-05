//go:build darwin || linux

package gateway_test

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func registerGatewayShutdownCheck(t *testing.T, p *gatewayProcess) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.command.Process.Signal(os.Interrupt)
			select {
			case <-p.done:
			case <-time.After(7 * time.Second):
				_ = p.command.Process.Signal(syscall.SIGQUIT)
				select {
				case <-p.done:
				case <-time.After(time.Second):
					_ = p.command.Process.Kill()
					<-p.done
				}
				t.Errorf("transport shutdown did not join: %s", p.stderr.String())
			}
		}
		if p.err != nil {
			t.Errorf("transport gateway exited: %v stderr=%s", p.err, p.stderr.String())
		}
	})
}
