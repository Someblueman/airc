package config

import "testing"

// Every descriptor is complete and named once.
func TestTransportsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, tr := range Transports() {
		if tr.Name == "" || seen[tr.Name] || tr.Kind == 0 || tr.Network == "" {
			t.Errorf("%q: unnamed, duplicate, or without kind or network", tr.Name)
		}
		seen[tr.Name] = true
		if tr.Address == 0 || tr.TLS == 0 || tr.Authors == 0 {
			t.Errorf("%s: without address shape, TLS policy or author model", tr.Name)
		}
		if (tr.Kind == Process) != (tr.Command != "" && tr.sole != 0 && tr.title != "") {
			t.Errorf("%s: a process carrier needs a command, title and exclusivity group, and only it", tr.Name)
		}
		if tr.unixSocket() != (tr.checkAddress != nil) {
			t.Errorf("%s: Unix socket paths, and only they, have an address check", tr.Name)
		}
	}
}
