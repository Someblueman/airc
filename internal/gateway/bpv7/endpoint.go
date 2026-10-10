package bpv7

import "strings"

// Endpoint restricts this profile to bounded lowercase dtn singleton EIDs.
// Administrative node EIDs end at the slash; application EIDs have one demux.
func Endpoint(eid string, node bool) bool {
	if !strings.HasPrefix(eid, "dtn://") || len(eid) > 256 {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(eid, "dtn://"), "/")
	if len(parts) != 2 || len(parts[0]) < 1 || len(parts[0]) > 128 {
		return false
	}
	const alpha = "abcdefghijklmnopqrstuvwxyz0123456789"
	if !strings.ContainsRune(alpha, rune(parts[0][0])) || strings.Trim(parts[0], alpha+".-") != "" {
		return false
	}
	if node {
		return parts[1] == ""
	}
	return len(parts[1]) > 0 && len(parts[1]) <= 64 && strings.ContainsRune(alpha, rune(parts[1][0])) && strings.Trim(parts[1], alpha+"_-") == ""
}

func (r *reader) endpoint(node bool) string {
	indefinite := r.array(2)
	if r.head(0) != 1 {
		r.err = ErrBundle
	}
	value := "dtn:" + string(r.bytes(3, 252))
	r.end(indefinite)
	if !Endpoint(value, node) {
		r.err = ErrBundle
	}
	return value
}

func (r *reader) nullEndpoint() {
	indefinite := r.array(2)
	if r.head(0) != 1 || r.head(0) != 0 {
		r.err = ErrBundle
	}
	r.end(indefinite)
}
