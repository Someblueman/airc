package protocol

import "testing"

func TestValidWakePrefix(t *testing.T) {
	for prefix, want := range map[string]bool{
		"https://ntfy.sh/":             true,
		"http://127.0.0.1:9000/hooks/": true,
		"https://hooks.example/airc/":  true,
		"https://ntfy.sh":              false,
		"https://hooks.example/airc":   false,
		"ftp://hooks.example/":         false,
		"https://u@hooks.example/":     false,
		"https://hooks.example/a?b/":   false,
		"https://hooks.example//":      false,
		"https://hooks.example/;a/":    false,
		"https://hooks.example/../":    false,
		"HTTPS://hooks.example/":       false,
	} {
		if got := ValidWakePrefix(prefix); got != want {
			t.Errorf("ValidWakePrefix(%q) = %v", prefix, got)
		}
	}
}

func TestPlainWakePath(t *testing.T) {
	for path, want := range map[string]bool{
		"/":                 true,
		"/topic":            true,
		"/airc/agent-7_x~y": true,
		"/airc/a.b":         true,
		"":                  false,
		"topic":             false,
		"/a//b":             false,
		"//evil.example/x":  false,
		"/a/..;/b":          false,
		"/a/./b":            false,
		"/a/.hidden":        false,
		"/a;b":              false,
		"/a%2fb":            false,
		"/a b":              false,
	} {
		if got := PlainWakePath(path); got != want {
			t.Errorf("PlainWakePath(%q) = %v", path, got)
		}
	}
}
