package auth

// Anonymous returns the permissions explicitly exposed by an enabled listener.
// The listener ID comes from owner configuration, never from request data.
// This identity describes a shared guest account, not an authenticated sender.
func (v *Verifier) Anonymous(listener string) (Identity, error) {
	if v == nil || v.anonymous[listener] == "" {
		return Identity{}, ErrAuthentication
	}
	for i := range v.principals {
		p := &v.principals[i]
		if p.id == v.anonymous[listener] && !p.revoked {
			return Identity{principal: p}, nil
		}
	}
	return Identity{}, ErrForbidden
}
