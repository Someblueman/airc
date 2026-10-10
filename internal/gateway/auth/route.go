package auth

// Route returns the principal that an owner-configured route posts as. The
// route, not the payload, establishes authority: anyone the transport lets
// write to that stream, channel, topic or address acts as this principal.
func (v *Verifier) Route(principal string) (Identity, error) {
	if v == nil || principal == "" {
		return Identity{}, ErrAuthentication
	}
	for i := range v.principals {
		p := &v.principals[i]
		if p.id == principal && !p.revoked {
			return Identity{principal: p}, nil
		}
	}
	return Identity{}, ErrAuthentication
}
