package protocol

import (
	"net/url"
	"strings"
)

// ValidWakePrefix reports whether prefix may be an entry of aircd's
// --wake-allow list: an http or https URL with a host and a path, so a
// prefix ends inside a host it names (https://ntfy.sh/ cannot match
// https://ntfy.sh.example/).
func ValidWakePrefix(prefix string) bool {
	u, err := url.Parse(prefix)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && strings.HasPrefix(u.Path, "/") && u.RawQuery == "" && u.Fragment == ""
}
