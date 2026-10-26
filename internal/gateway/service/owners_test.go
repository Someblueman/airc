package service

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// Every transport serve owns has an owner, and nothing else does: a new
// Stream or Packet descriptor fails here until its constructor is added.
func TestOwnersCoverTheRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, tr := range config.Transports() {
		seen[tr.Name] = true
		_, owned := owners[tr.Name]
		switch tr.Kind {
		case config.Stream, config.Packet:
			if !owned {
				t.Errorf("%s: serve has no owner", tr.Name)
			}
		case config.Process:
			if owned {
				t.Errorf("%s: a process carrier is owned by serve", tr.Name)
			}
			if tr.Command == "" || tr.ServeRefusal() == nil {
				t.Errorf("%s: no dedicated command", tr.Name)
			}
		default:
			t.Errorf("%s: no kind", tr.Name)
		}
	}
	for name := range owners {
		if !seen[name] {
			t.Errorf("owner %s has no transport", name)
		}
	}
}
