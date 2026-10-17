// Package backend owns bounded public-SDK attempts, with no reconnect or account
// creation. Only shared dispatch/worker paths call it; adapters use those paths.
package backend

import (
	"context"
	"crypto/tls"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// upstream is the AIRC endpoint every author session dials: its pinned
// configuration and dial template. Who logs in is filled per job.
type upstream struct {
	pinned config.Upstream
	dial   irc.Config
	// gateway is aircd's gateway credential: with it, every author session
	// asserts its client's source with WEBIRC.
	gateway string
}

type Backend struct {
	realm                        string
	carriers                     []string
	lookupActive                 map[string]int
	lookupTotal                  int
	watchActive                  map[string]int
	watchTotal                   int
	watchLimit, authorWatchLimit int
	mu                           sync.Mutex
	upstream                     *upstream
	active                       map[string]int
	total                        int
	globalLimit, authorLimit     int
	// waitTotal and waitActive count checks waiting for news, within
	// waitLimit (limits.waits).
	waitTotal  int
	waitActive map[string]int
	waitLimit  int
	// freed, made by a read waiting for a slot, is closed when one frees.
	freed chan struct{}
	// pool keeps sessions that finished a request cleanly for the same
	// author's next one; see sessionPool.
	pool *sessionPool
	// followLease and followKeepAlive shape followed checks (Follow); a
	// zero lease refuses them.
	followLease, followKeepAlive time.Duration
}

// Outcome contains evidence after all connection work has stopped. Observed
// acceptance must be saved through store.Accept; a failure through Complete.
// Journal errors are not permission to repeat network work with this consumed
// grant.
type Outcome struct {
	Attempt store.Attempt
	// Acceptance is AIRC's acknowledgement, already bound to the job's
	// intent; it is empty for a failure.
	core.Acceptance
	// AccountID is the AIRC account a registered author's session logged
	// in as, when AIRC reported it; the caller records it for diagnosis.
	AccountID  string
	ObservedAt time.Time
	Completion store.Completion
}

// Realm is the immutable configured scope, used to bind read cursors.
func (b *Backend) Realm() string { return b.realm }

// Carriers is the configured advertised carrier list, in preference order.
func (b *Backend) Carriers() []string { return slices.Clone(b.carriers) }

// New reads owner-provisioned credentials without contacting AIRC. References,
// tokens and TLS policy are an immutable startup snapshot; rotation takes effect
// on a new backend. Nothing creates accounts or changes a running daemon.
func New(c config.Config) (*Backend, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := c.CheckFiles(); err != nil {
		return nil, err
	}
	b := &Backend{realm: c.Realm, carriers: slices.Clone(c.Carriers), active: map[string]int{}, lookupActive: map[string]int{}, globalLimit: c.Limits.Sessions, authorLimit: c.Limits.SessionsPerAuthor, watchActive: map[string]int{}, watchLimit: c.Limits.Watches, authorWatchLimit: c.Limits.WatchesPerAuthor, pool: newSessionPool(c.Limits.IdleSessions, c.Limits.SessionsPerAuthor), waitActive: map[string]int{}, waitLimit: c.Limits.Waits, followLease: time.Duration(c.Limits.FollowSeconds) * time.Second, followKeepAlive: time.Duration(c.Limits.FollowKeepAliveSeconds) * time.Second}
	if entry, ok := c.AuthorUpstream(); ok {
		dial, err := dialTemplate(entry)
		if err != nil {
			return nil, err
		}
		u := upstream{pinned: entry, dial: dial}
		if entry.GatewayTokenFile != "" {
			if u.gateway, err = auth.ReadToken(entry.GatewayTokenFile); err != nil {
				return nil, auth.ErrCredential
			}
		}
		b.upstream = &u
	}
	return b, nil
}

// Close ends the sessions kept for reuse. Requests may still run; their
// sessions close when they finish.
func (b *Backend) Close() { b.pool.close() }

// dialTemplate is the transport half of an AIRC session: endpoint, TLS policy
// and AIRC access token. Who logs in is filled per job by sessionConfig.
func dialTemplate(entry config.Upstream) (irc.Config, error) {
	client := irc.Config{Network: entry.Network, Addr: entry.Address, Ephemeral: true, ReadTimeout: 10 * time.Second, WriteTimeout: 5 * time.Second}
	var err error
	if entry.AccessTokenFile != "" {
		client.AccessToken, err = auth.ReadToken(entry.AccessTokenFile)
		if err != nil {
			return irc.Config{}, auth.ErrCredential
		}
	}
	if entry.TLSServerName != "" {
		client.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, ServerName: entry.TLSServerName}
		if entry.TLSCAFile != "" {
			client.TLSConfig.RootCAs, err = auth.ReadTrustRoots(entry.TLSCAFile)
			if err != nil {
				return irc.Config{}, auth.ErrCredential
			}
		}
	}
	return client, nil
}

// sessionConfig builds one job's AIRC session from its author. Every session
// is ephemeral. A guest sends no token, so pkg/irc skips SASL and AIRC applies
// its guest rules (498 on a registered nickname); a registered author logs in
// with its own token. It reports false when the upstream or credential is
// unavailable.
func (b *Backend) sessionConfig(pinned config.Upstream, author core.Author, credential core.Credential, source string) (irc.Config, bool) {
	if !author.Valid() || b.upstream == nil || b.upstream.pinned != pinned || author.Registered == credential.Empty() {
		return irc.Config{}, false
	}
	cfg := b.upstream.dial
	cfg.IdentityToken = credential.Token()
	if b.upstream.gateway != "" {
		if !protocol.ValidSource(source) {
			return irc.Config{}, false
		}
		cfg.GatewayToken, cfg.Source = b.upstream.gateway, source
	}
	cfg.Nick = author.Nick
	cfg.Ephemeral = true
	cfg.CreateAccount = false
	return cfg, true
}

// readSlotWait is how long a read waits for a free session slot before
// answering capacity_exceeded, so a burst (many followers woken by one
// message) queues briefly instead of failing.
const readSlotWait = time.Second

// acquireRead is acquire for a read, waiting up to readSlotWait (or until
// ctx ends) for a slot to free.
func (b *Backend) acquireRead(ctx context.Context, lane string) bool {
	timer := time.NewTimer(readSlotWait)
	defer timer.Stop()
	for {
		b.mu.Lock()
		ok := b.acquireLocked(lane, store.LookupAttempt)
		if b.freed == nil {
			b.freed = make(chan struct{})
		}
		freed := b.freed
		b.mu.Unlock()
		if ok {
			return true
		}
		select {
		case <-freed:
		case <-timer.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
}

func (b *Backend) acquire(lane string, kind store.AttemptKind) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.acquireLocked(lane, kind)
}

func (b *Backend) acquireLocked(lane string, kind store.AttemptKind) bool {
	if b.total >= b.globalLimit || b.active[lane] >= b.authorLimit {
		return false
	}
	if kind == store.LookupAttempt {
		if b.lookupTotal >= b.globalLimit-1 || b.lookupActive[lane] >= b.authorLimit-1 {
			return false
		}
		b.lookupTotal++
		b.lookupActive[lane]++
	}
	b.total++
	b.active[lane]++
	return true
}

func (b *Backend) release(lane string, kind store.AttemptKind) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if kind == store.LookupAttempt {
		b.lookupTotal--
		b.lookupActive[lane]--
	}
	b.total--
	b.active[lane]--
	// Wake reads waiting for a slot.
	if b.freed != nil {
		close(b.freed)
		b.freed = nil
	}
}

// upstreamIdentity reports whether a job's stored upstream request ID is the
// one its author scope and request ID derive: the request ID itself.
func (b *Backend) upstreamIdentity(j store.Job) bool {
	current, err := core.UpstreamID(j.Scope, j.Request.RequestID)
	return err == nil && j.UpstreamID == current
}

func failed(a store.Attempt, code core.ErrorCode, transient, mayHavePosted bool) Outcome {
	return Outcome{Attempt: a, Completion: store.Completion{Failure: core.Failure{Code: code, Transient: transient}, MayHavePosted: mayHavePosted}}
}

// Run consumes one grant, uses at most one exclusive session (a kept one of
// the same author, or a new one), and closes or keeps it before returning;
// only a session whose mutation AIRC acknowledged is kept. The caller's context belongs to the daemon worker, not
// the ingress waiter. Saturation returns bounded backpressure without a queue.
func (b *Backend) Run(ctx context.Context, g *store.Grant) (outcome Outcome, failure error) {
	var accountID string
	defer func() {
		if a := outcome.Attempt; failure == nil && accountID != "" && a.Job.Author.Registered {
			outcome.AccountID = accountID
		}
	}()
	a, err := g.Consume(time.Now())
	if err != nil {
		return Outcome{}, err
	}
	if a.Realm != b.realm || !b.upstreamIdentity(a.Job) || a.Job.Author.Scope() != a.Job.Scope {
		return failed(a, core.Forbidden, false, false), nil
	}
	session, ok := b.sessionConfig(a.Backend, a.Job.Author, a.Credential, a.Source)
	if !ok {
		return failed(a, core.Forbidden, false, false), nil
	}
	m, ok := mutations[a.Job.Request.Operation]
	if !ok {
		return failed(a, core.Forbidden, false, false), nil
	}
	// Without a native request-ID lookup, a lookup grant must never become a
	// write: only a later send grant, allowed by the stored retry policy, may
	// repeat the exact tuple with the journal's duplicate-risk marker.
	if !m.lookup && a.Kind == store.LookupAttempt {
		return failed(a, core.DeliveryUncertain, true, false), nil
	}
	lane := a.Job.Lane()
	if !b.acquire(lane, a.Kind) {
		return failed(a, core.CapacityExceeded, true, false), nil
	}
	defer b.release(lane, a.Kind)
	overall, cancel := context.WithDeadline(ctx, a.Deadline)
	defer cancel()
	key := sessionKey(session, a.Job.Scope)
	client, dialed := b.pool.take(key, session.Nick, !a.Job.Author.Registered)
	if client == nil {
		dialCtx, stopDial := context.WithTimeout(overall, 10*time.Second)
		client, err = irc.DialContext(dialCtx, session)
		stopDial()
		dialed = time.Now()
	}
	if err != nil {
		var rejected *irc.RejectedError
		var verification *tls.CertificateVerificationError
		if errors.As(err, &verification) {
			return failed(a, core.AuthenticationFailed, false, false), nil
		}
		if errors.As(err, &rejected) {
			return registrationFailure(a, rejected), nil
		}
		var inUse *irc.NickInUseError
		if errors.As(err, &inUse) {
			o := failed(a, core.NickInUse, false, false)
			o.Completion.Failure.UpstreamCode = 433
			return o, nil
		}
		return failed(a, core.UpstreamUnavailable, true, false), nil
	}
	closed := make(chan struct{})
	stopClose := context.AfterFunc(overall, func() { _ = client.Close(); close(closed) })
	// Only a session whose mutation AIRC acknowledged is kept: after any
	// failure its state is unknown.
	reuse := false
	defer func() {
		if !stopClose() {
			<-closed
		} else if reuse {
			b.pool.put(key, client, dialed)
			return
		}
		_ = client.Close()
	}()
	accountID = client.AccountID()
	if !client.Ephemeral() {
		return failed(a, core.UnsupportedOperation, false, false), nil
	}
	features := client.Features()
	for _, feature := range m.features {
		if features[feature] != "1" {
			return failed(a, core.UnsupportedOperation, false, false), nil
		}
	}
	o := m.exchange(overall, client, a)
	reuse = o.Acceptance != (core.Acceptance{})
	return o, nil
}

// mutation is how one durable operation runs against AIRC: whether a lookup
// grant may ask about an earlier send, the features AIRC must advertise, and
// the exchange that writes the command and recognises its acknowledgement.
type mutation struct {
	lookup   bool
	features []string
	exchange func(context.Context, *irc.Client, store.Attempt) Outcome
}

var mutations = map[core.Operation]mutation{
	core.Send:  {lookup: true, features: []string{"RECEIPTS", "IDEMPOTENCY", "SAFE_RETRY"}, exchange: sendExchange},
	core.Reply: {lookup: true, features: []string{"RECEIPTS", "IDEMPOTENCY", "SAFE_RETRY", "REPLIES", "CHAT", "CONTEXT", "CONTEXT_BYTES"}, exchange: parentExchange},
	// REACT has no native request-ID lookup.
	core.ReactionAdd:       {features: []string{"RECEIPTS", "REACTIONS", "REPLIES", "CHAT", "CONTEXT", "CONTEXT_BYTES"}, exchange: reactionExchange},
	core.ProfileUpdate:     {features: []string{"DIRECTORY"}, exchange: cardExchange},
	core.PresenceSet:       {features: []string{"DIRECTORY"}, exchange: cardExchange},
	core.TopicSet:          {exchange: topicExchange}, // TOPIC is a baseline command, like topic.read.
	core.RoomUpdate:        {features: []string{"CHAT"}, exchange: roomExchange},
	core.PinsAdd:           {features: []string{"CHAT", "CONTEXT", "CONTEXT_BYTES"}, exchange: pinExchange},
	core.PinsRemove:        {features: []string{"CHAT", "CONTEXT", "CONTEXT_BYTES"}, exchange: pinExchange},
	core.ActivitySet:       {features: []string{"CHAT"}, exchange: activityExchange},
	core.ReplySignalSet:    {features: []string{"CHAT", "CONTEXT", "CONTEXT_BYTES"}, exchange: replySignalExchange},
	core.ReplySignalCancel: {features: []string{"CHAT", "CONTEXT", "CONTEXT_BYTES"}, exchange: replySignalExchange},
	core.PollCreate:        {features: []string{"CHAT"}, exchange: pollMutationExchange},
	core.PollVote:          {features: []string{"CHAT"}, exchange: pollMutationExchange},
	core.PollClose:         {features: []string{"CHAT"}, exchange: pollMutationExchange},
	core.MessageAction:     {features: []string{"CHAT"}, exchange: chatPostExchange},
	core.MessageCorrect:    {features: []string{"CHAT"}, exchange: chatPostExchange},
	core.MessageRetract:    {features: []string{"CHAT"}, exchange: chatPostExchange},
}

// accepted is an exchange's outcome once AIRC acknowledged the mutation: the
// evidence must bind to the stored intent and be observed before both the
// acknowledgement wait's and the attempt's deadlines.
func accepted(wait context.Context, a store.Attempt, evidence core.Acceptance, author string) Outcome {
	if _, err := evidence.Encode(a.Job.Request, a.Job.UpstreamID, author); err != nil {
		return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
	}
	observed := time.Now()
	deadline, bounded := wait.Deadline()
	if wait.Err() != nil || bounded && !observed.Before(deadline) || !observed.Before(a.Deadline) {
		return failed(a, core.DeliveryUncertain, true, a.Kind == store.SendAttempt)
	}
	return Outcome{Attempt: a, Acceptance: evidence, ObservedAt: observed}
}
