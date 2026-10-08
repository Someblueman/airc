package xmppwire

import "strings"

// BareJID selects canonical lowercase ASCII localpart@DNS-domain addresses.
// This provisioning subset avoids implicit Unicode/case/IDNA aliases. Full
// RFC 7622 internationalized address preparation is outside this profile.
func BareJID(s string) bool {
	if len(s) > 318 || s != strings.ToLower(s) {
		return false
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || len(local) < 1 || len(local) > 64 || len(domain) < 1 || len(domain) > 253 {
		return false
	}
	for _, c := range local {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	for label := range strings.SplitSeq(domain, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// FullJID retains the case-sensitive resource. The bridge uses the bare address
// for provisioned identity and the exact resource for membership observations.
func FullJID(s string) (string, string, bool) {
	bare, resource, ok := strings.Cut(s, "/")
	if !ok || !BareJID(bare) || len(resource) < 1 || len(resource) > 64 {
		return "", "", false
	}
	for _, c := range resource {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return "", "", false
		}
	}
	return bare, resource, true
}
