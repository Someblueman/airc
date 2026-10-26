package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

const (
	// idleTimeout closes a kept session nobody reused, well inside the
	// upstream read timeout (dialTemplate's 10 s) that would end it anyway.
	idleTimeout = 5 * time.Second
	// sessionLifetime bounds how long one AIRC login serves an author. A
	// fresh session re-checks what AIRC checks only at registration: a
	// registered author's token, and that no connected client holds a
	// guest's nickname. Reuse checks the nickname each time (see resume),
	// but WHO does not show every holder AIRC refuses guests for, and a
	// revoked token takes effect at the next login, so neither may wait
	// longer than this.
	sessionLifetime = 30 * time.Second
	// resumeTimeout bounds the round trip that readies a kept session.
	resumeTimeout = time.Second
)

// sessionPool keeps AIRC sessions that finished a request cleanly, so the
// same author's next request skips a new connection and login. A session
// is only ever reused under the key it was dialled with: the same author
// scope, credential and client source. Idle sessions do not count toward
// limits.sessions, which bounds sessions in use; they have their own bound.
type sessionPool struct {
	mu        sync.Mutex
	idle      map[string][]*keptSession // oldest first
	count     int
	limit     int
	perAuthor int
	reaper    *time.Timer
	closed    bool
	sequence  atomic.Uint64
}

type keptSession struct {
	client *irc.Client
	dialed time.Time
	idle   time.Time
}

func newSessionPool(limit, perAuthor int) *sessionPool {
	return &sessionPool{idle: map[string][]*keptSession{}, limit: limit, perAuthor: perAuthor}
}

// sessionKey names who a session is logged in as, from its dial config.
// The token is kept only as a digest.
func sessionKey(c irc.Config, scope string) string {
	digest := sha256.Sum256([]byte(c.IdentityToken))
	return scope + "\x00" + hex.EncodeToString(digest[:]) + "\x00" + c.Source
}

// take returns a kept session for key, readied by resume, or nil. dialed
// is when it logged in, for a later put.
func (p *sessionPool) take(key, nick string, guest bool) (client *irc.Client, dialed time.Time) {
	if p == nil || p.limit == 0 {
		return nil, time.Time{}
	}
	for {
		p.mu.Lock()
		list := p.idle[key]
		if len(list) == 0 {
			p.mu.Unlock()
			return nil, time.Time{}
		}
		kept := list[len(list)-1]
		if len(list) == 1 {
			delete(p.idle, key)
		} else {
			p.idle[key] = list[:len(list)-1]
		}
		p.count--
		p.mu.Unlock()
		now := time.Now()
		if now.Sub(kept.idle) < idleTimeout && now.Sub(kept.dialed) < sessionLifetime && p.resume(kept.client, nick, guest) {
			return kept.client, kept.dialed
		}
		_ = kept.client.Close()
	}
}

// resume readies a kept session for a new request: it discards whatever
// arrived while it was idle, then pings, so every reply to an earlier
// command has been read before the request's own command owns the session
// (exchanges attribute any error numeric to their command). For a guest it
// also asks WHO for its nickname, as a fresh guest login would be refused
// while a connected client holds it.
func (p *sessionPool) resume(client *irc.Client, nick string, guest bool) bool {
	if !alive(client) {
		return false
	}
	for {
		select {
		case _, ok := <-client.Events():
			if !ok {
				return false
			}
			continue
		default:
		}
		break
	}
	token := "airc-gw-" + strconv.FormatUint(p.sequence.Add(1), 36)
	if err := client.Raw("PING :" + token); err != nil {
		return false
	}
	if guest {
		if err := client.Raw("WHO " + nick); err != nil {
			return false
		}
	}
	timer := time.NewTimer(resumeTimeout)
	defer timer.Stop()
	ponged := false
	for {
		select {
		case <-timer.C:
			return false
		case event, ok := <-client.Events():
			if !ok {
				return false
			}
			switch e := event.(type) {
			case *irc.RawEvent:
				if e.Command == "PONG" && e.Trailing == token {
					if !guest {
						return true
					}
					ponged = true
				}
			case *irc.PresenceEvent:
				if ponged && strings.EqualFold(e.Nick, nick) {
					return false // a connected client holds the nickname
				}
			case *irc.EndOfWhoEvent:
				if ponged && strings.EqualFold(e.Target, nick) {
					return true
				}
			}
		}
	}
}

// alive reports a session neither closed nor lost. Err alone reads nil
// after Close.
func alive(client *irc.Client) bool {
	select {
	case <-client.Done():
		return false
	default:
		return client.Err() == nil
	}
}

// put keeps a session that finished its request cleanly, or closes it. The
// caller must have stopped every callback that could close it.
func (p *sessionPool) put(key string, client *irc.Client, dialed time.Time) {
	now := time.Now()
	p.mu.Lock()
	if p.closed || p.count >= p.limit || len(p.idle[key]) >= p.perAuthor || !alive(client) || now.Sub(dialed) >= sessionLifetime {
		p.mu.Unlock()
		_ = client.Close()
		return
	}
	p.idle[key] = append(p.idle[key], &keptSession{client: client, dialed: dialed, idle: now})
	p.count++
	if p.reaper == nil {
		p.reaper = time.AfterFunc(idleTimeout, p.reap)
	}
	p.mu.Unlock()
}

// reap closes sessions past their idle or lifetime bound, and rearms while
// any remain.
func (p *sessionPool) reap() {
	now := time.Now()
	var expired []*irc.Client
	p.mu.Lock()
	for key, list := range p.idle {
		kept := list[:0]
		for _, s := range list {
			if now.Sub(s.idle) >= idleTimeout || now.Sub(s.dialed) >= sessionLifetime {
				expired = append(expired, s.client)
				continue
			}
			kept = append(kept, s)
		}
		p.count -= len(list) - len(kept)
		if len(kept) == 0 {
			delete(p.idle, key)
		} else {
			p.idle[key] = kept
		}
	}
	p.reaper = nil
	if p.count > 0 && !p.closed {
		p.reaper = time.AfterFunc(idleTimeout/2, p.reap)
	}
	p.mu.Unlock()
	for _, client := range expired {
		_ = client.Close()
	}
}

// close ends every kept session; later puts close theirs.
func (p *sessionPool) close() {
	if p == nil {
		return
	}
	var all []*irc.Client
	p.mu.Lock()
	p.closed = true
	if p.reaper != nil {
		p.reaper.Stop()
		p.reaper = nil
	}
	for _, list := range p.idle {
		for _, s := range list {
			all = append(all, s.client)
		}
	}
	clear(p.idle)
	p.count = 0
	p.mu.Unlock()
	for _, client := range all {
		_ = client.Close()
	}
}
