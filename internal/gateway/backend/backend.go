// Package backend owns bounded public-SDK attempts, with no reconnect or account
// creation. Only shared dispatch/worker paths call it; adapters use those paths.
package backend

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// binding is a principal-shim upstream: its dial template and the binding
// account's token. Per-job authors use only the shared upstream template.
type binding struct {
	pinned config.Binding
	dial   irc.Config
	token  string
}

type Backend struct {
	realm                           string
	lookupActive                    map[string]int
	lookupTotal                     int
	watchActive                     map[string]int
	watchTotal                      int
	watchLimit, principalWatchLimit int
	mu                              sync.Mutex
	bindings                        map[string]binding
	upstream                        *binding
	principals                      map[string]string
	active                          map[string]int
	total                           int
	globalLimit, principalLimit     int
}

// Outcome contains evidence after all connection work has stopped. A receipt
// must be saved through RecordAcceptance; a failure through Complete. Journal
// errors are not permission to repeat network work with this consumed grant.
type Outcome struct {
	Attempt     store.Attempt
	Receipt     *irc.SendReceiptEvent
	Card        *core.CardAcknowledgement
	Topic       *core.TopicAcknowledgement
	Room        *core.RoomAcknowledgement
	Pin         *core.PinAcknowledgement
	Activity    *core.ActivityAcknowledgement
	ReplySignal *core.ReplySignalAcknowledgement
	Poll        *core.PollAcknowledgement
	ObservedAt  time.Time
	Completion  store.Completion
}

// Realm is the immutable configured scope, used to bind read cursors.
func (b *Backend) Realm() string { return b.realm }

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
	b := &Backend{realm: c.Realm, bindings: map[string]binding{}, principals: map[string]string{}, active: map[string]int{}, lookupActive: map[string]int{}, globalLimit: c.Limits.Sessions, principalLimit: c.Limits.SessionsPerPrincipal, watchActive: map[string]int{}, watchLimit: c.Limits.Watches, principalWatchLimit: c.Limits.WatchesPerPrincipal}
	for _, p := range c.Principals {
		b.principals[p.ID] = p.Binding
	}
	for _, entry := range c.Bindings {
		token, err := auth.ReadToken(entry.AccountTokenFile)
		if err != nil {
			return nil, auth.ErrCredential
		}
		dial, err := dialTemplate(entry)
		if err != nil {
			return nil, err
		}
		b.bindings[entry.ID] = binding{pinned: entry, dial: dial, token: token}
	}
	if entry, ok := c.Upstream(); ok {
		upstream := b.bindings[entry.ID]
		upstream.token = "" // an author never inherits a binding's account
		b.upstream = &upstream
	}
	return b, nil
}

// dialTemplate is the transport half of an AIRC session: endpoint, TLS policy
// and AIRC access token. Who logs in is filled per job by sessionConfig.
func dialTemplate(entry config.Binding) (irc.Config, error) {
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
// with its own token, or for the principal shim, the binding account's token.
// It reports false when the job's upstream or credential is unavailable.
func (b *Backend) sessionConfig(principal, bindingID string, pinned config.Binding, author core.Author, credential core.Credential) (irc.Config, bool) {
	if !author.Valid() {
		return irc.Config{}, false
	}
	var cfg irc.Config
	if principal != "" {
		entry, ok := b.bindings[bindingID]
		if !ok || b.principals[principal] != bindingID || entry.pinned != pinned || !author.Registered || author.Nick != entry.pinned.Account {
			return irc.Config{}, false
		}
		cfg = entry.dial
		cfg.IdentityToken = entry.token
	} else {
		if b.upstream == nil || b.upstream.pinned != pinned || author.Registered == credential.Empty() {
			return irc.Config{}, false
		}
		cfg = b.upstream.dial
		cfg.IdentityToken = credential.Token()
	}
	cfg.Nick = author.Nick
	cfg.Ephemeral = true
	cfg.CreateAccount = false
	return cfg, true
}

func (b *Backend) acquire(principal string, kind store.AttemptKind) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total >= b.globalLimit || b.active[principal] >= b.principalLimit {
		return false
	}
	if kind == store.LookupAttempt {
		if b.lookupTotal >= b.globalLimit-1 || b.lookupActive[principal] >= b.principalLimit-1 {
			return false
		}
		b.lookupTotal++
		b.lookupActive[principal]++
	}
	b.total++
	b.active[principal]++
	return true
}

func (b *Backend) release(principal string, kind store.AttemptKind) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if kind == store.LookupAttempt {
		b.lookupTotal--
		b.lookupActive[principal]--
	}
	b.total--
	b.active[principal]--
}

func failed(a store.Attempt, code core.ErrorCode, transient, mayHavePosted bool) Outcome {
	return Outcome{Attempt: a, Completion: store.Completion{Failure: core.Failure{Code: code, Transient: transient}, MayHavePosted: mayHavePosted}}
}

// Run consumes one grant, opens at most one exclusive session, and closes/joins
// it before returning. The caller's context belongs to the daemon worker, not
// the ingress waiter. Saturation returns bounded backpressure without a queue.
func (b *Backend) Run(ctx context.Context, g *store.Grant) (Outcome, error) {
	a, err := g.Consume(time.Now())
	if err != nil {
		return Outcome{}, err
	}
	expectedID, idErr := core.UpstreamID(b.realm, a.Job.Scope, a.Job.Request.RequestID)
	if idErr != nil || expectedID != a.Job.UpstreamID || a.Job.Author.Scope() != a.Job.Scope {
		return failed(a, core.Forbidden, false, false), nil
	}
	session, ok := b.sessionConfig(a.Job.Principal, a.Job.Binding, a.Backend, a.Job.Author, a.Credential)
	if !ok {
		return failed(a, core.Forbidden, false, false), nil
	}
	// REACT has no native request-ID lookup. A lookup grant must never become
	// a write: only a later send grant, allowed by the stored retry policy,
	// may repeat the exact tuple with the journal's duplicate-risk marker.
	if (a.Job.Request.Operation == core.ReactionAdd || a.Job.Request.Operation == core.ProfileUpdate || a.Job.Request.Operation == core.PresenceSet || a.Job.Request.Operation == core.TopicSet || a.Job.Request.Operation == core.RoomUpdate || a.Job.Request.Operation == core.PinsAdd || a.Job.Request.Operation == core.PinsRemove || a.Job.Request.Operation == core.ActivitySet || a.Job.Request.Operation == core.ReplySignalSet || a.Job.Request.Operation == core.ReplySignalCancel || a.Job.Request.Operation == core.PollCreate || a.Job.Request.Operation == core.PollVote || a.Job.Request.Operation == core.PollClose) && a.Kind == store.LookupAttempt {
		return failed(a, core.DeliveryUncertain, true, false), nil
	}
	if (a.Job.Request.Operation == core.MessageAction || a.Job.Request.Operation == core.MessageCorrect || a.Job.Request.Operation == core.MessageRetract) && a.Kind == store.LookupAttempt {
		return failed(a, core.DeliveryUncertain, true, false), nil
	}
	lane := a.Job.Lane()
	if !b.acquire(lane, a.Kind) {
		return failed(a, core.CapacityExceeded, true, false), nil
	}
	defer b.release(lane, a.Kind)
	overall, cancel := context.WithDeadline(ctx, a.Deadline)
	defer cancel()
	dialCtx, stopDial := context.WithTimeout(overall, 10*time.Second)
	client, err := irc.DialContext(dialCtx, session)
	stopDial()
	if err != nil {
		var rejected *irc.RejectedError
		var verification *tls.CertificateVerificationError
		if errors.As(err, &verification) {
			return failed(a, core.AuthenticationFailed, false, false), nil
		}
		if errors.As(err, &rejected) {
			return registrationFailure(a, rejected), nil
		}
		return failed(a, core.UpstreamUnavailable, true, false), nil
	}
	closed := make(chan struct{})
	stopClose := context.AfterFunc(overall, func() { _ = client.Close(); close(closed) })
	defer func() {
		if !stopClose() {
			<-closed
		}
		_ = client.Close()
	}()
	if !client.Ephemeral() {
		return failed(a, core.UnsupportedOperation, false, false), nil
	}
	features := client.Features()
	required := []string{"RECEIPTS", "IDEMPOTENCY", "SAFE_RETRY"}
	cardMutation := a.Job.Request.Operation == core.ProfileUpdate || a.Job.Request.Operation == core.PresenceSet
	topicMutation := a.Job.Request.Operation == core.TopicSet
	roomMutation := a.Job.Request.Operation == core.RoomUpdate
	pinMutation := a.Job.Request.Operation == core.PinsAdd || a.Job.Request.Operation == core.PinsRemove
	activityMutation := a.Job.Request.Operation == core.ActivitySet
	replySignalMutation := a.Job.Request.Operation == core.ReplySignalSet || a.Job.Request.Operation == core.ReplySignalCancel
	pollMutation := a.Job.Request.Operation == core.PollCreate || a.Job.Request.Operation == core.PollVote || a.Job.Request.Operation == core.PollClose
	chatPostMutation := a.Job.Request.Operation == core.MessageAction || a.Job.Request.Operation == core.MessageCorrect || a.Job.Request.Operation == core.MessageRetract
	if cardMutation {
		required = []string{"DIRECTORY"}
	}
	if topicMutation {
		required = nil // TOPIC is a baseline command, like topic.read.
	}
	if roomMutation {
		required = []string{"CHAT"}
	}
	if pinMutation {
		required = []string{"CHAT", "CONTEXT", "CONTEXT_BYTES"}
	}
	if activityMutation {
		required = []string{"CHAT"}
	}
	if replySignalMutation {
		required = []string{"CHAT", "CONTEXT", "CONTEXT_BYTES"}
	}
	if pollMutation {
		required = []string{"CHAT"}
	}
	if chatPostMutation {
		required = []string{"CHAT"}
	}
	if a.Job.Request.Operation == core.ReactionAdd {
		required = []string{"RECEIPTS", "REACTIONS"}
		if !builtinReaction(a.Job.Request.Reaction) {
			required = append(required, "CUSTOM_REACTIONS")
		}
	}
	for _, feature := range required {
		if features[feature] != "1" {
			return failed(a, core.UnsupportedOperation, false, false), nil
		}
	}
	if cardMutation {
		return cardExchange(overall, client, a), nil
	}
	if topicMutation {
		return topicExchange(overall, client, a), nil
	}
	if roomMutation {
		return roomExchange(overall, client, a), nil
	}
	if pinMutation {
		return pinExchange(overall, client, a), nil
	}
	if activityMutation {
		return activityExchange(overall, client, a), nil
	}
	if replySignalMutation {
		return replySignalExchange(overall, client, a), nil
	}
	if pollMutation {
		return pollMutationExchange(overall, client, a), nil
	}
	if chatPostMutation {
		return chatPostExchange(overall, client, a), nil
	}
	var root string
	if a.Job.Request.Operation == core.Reply || a.Job.Request.Operation == core.ReactionAdd {
		for _, feature := range []string{"REPLIES", "CHAT", "CONTEXT", "CONTEXT_BYTES"} {
			if features[feature] != "1" {
				return failed(a, core.UnsupportedOperation, false, false), nil
			}
		}
		var rejection *Outcome
		root, rejection = authorizeParent(overall, client, a)
		if rejection != nil {
			return *rejection, nil
		}
	}
	return exchange(overall, client, a, root), nil
}
