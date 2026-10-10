package protocol

import "net"

// MaxSourceLength bounds a WEBIRC source label.
const MaxSourceLength = 64

// ValidSource reports whether source may be asserted with WEBIRC: an IP
// address, or a label for a carrier without addresses such as
// "listener:mqtt". A label starts with a letter and continues with letters,
// digits and ._:/-, so an address with a port or zone is not mistaken for one.
func ValidSource(source string) bool {
	if net.ParseIP(source) != nil {
		return true
	}
	if len(source) == 0 || len(source) > MaxSourceLength {
		return false
	}
	for i, r := range source {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if letter || i > 0 && (r >= '0' && r <= '9' || r == '.' || r == '_' || r == ':' || r == '/' || r == '-') {
			continue
		}
		return false
	}
	return true
}
