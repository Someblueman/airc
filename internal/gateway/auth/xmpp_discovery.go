package auth

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// discoverRoom is ordered on the authenticated session before room entry. It
// shares the caller's absolute deadline; buffered replies cannot renew it.
func (x *XMPPSession) discoverRoom(room string, deadline time.Time, count *int) error {
	if !x.usable() || !time.Now().Before(deadline) {
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
	for {
		if count == nil || *count >= 512 {
			return ErrAuthentication
		}
		*count = *count + 1
		e, err := x.reader.Read()
		if err != nil || !x.usable() || !time.Now().Before(deadline) {
			return ErrAuthentication
		}
		if handled, err := x.joinControl(e); err != nil {
			return ErrAuthentication
		} else if handled {
			continue
		}
		if !xmppwire.Discovered(e, room, x.server.account+"/"+x.server.resource, id) {
			return ErrAuthentication
		}
		return nil
	}
}
