package protocol

import (
	"net/url"
	"strings"
)

// ValidWakePrefix reports whether prefix may be an entry of aircd's
// --wake-allow list: an http or https URL with a host and a plain path
// ending in "/", so a prefix ends inside a host it names (https://ntfy.sh/
// cannot match https://ntfy.sh.example/) and at a path segment
// (https://hooks.example/airc/ cannot match /aircx).
func ValidWakePrefix(prefix string) bool {
	u, err := url.Parse(prefix)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && strings.HasSuffix(u.Path, "/") && PlainWakePath(u.Path) && u.String() == prefix && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

// PlainWakePath reports a path that every web server resolves to itself:
// it starts with "/", holds only letters, digits and "-._~/", and has no
// empty segment ("//") or segment starting with "." save a trailing "/",
// so no receiver can read a dot segment, a ";" parameter or an escape as a
// way out of the prefix it starts with.
func PlainWakePath(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	segments := strings.Split(path[1:], "/")
	for i, segment := range segments {
		if segment == "" && i != len(segments)-1 || strings.HasPrefix(segment, ".") {
			return false
		}
		for _, r := range segment {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
				return false
			}
		}
	}
	return true
}
