package auth

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// discoverRoom is ordered on the authenticated session before room entry. It
// shares the caller's absolute deadline; buffered replies cannot renew it.
func (x *XMPPSession) discoverRoom(room string, deadline time.Time) error {
	if !time.Now().Before(deadline) {
		return ErrAuthentication
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrAuthentication
	}
	id := hex.EncodeToString(nonce[:])
	w, err := xmppwire.Discover(room, id)
	if err != nil || x.write(w) != nil {
		return ErrAuthentication
	}
	e, err := x.reader.Read()
	if err != nil || !time.Now().Before(deadline) || !xmppwire.Discovered(e, room, x.server.account+"/"+x.server.resource, id) {
		return ErrAuthentication
	}
	return nil
}
