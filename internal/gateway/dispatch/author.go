package dispatch

import (
	"context"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
)

// Session is one connection's author state on a connection-based carrier.
// After an account.identify the identified author is the default for the
// connection's later requests; after a correct access password the
// connection is admitted. It lives in memory for the connection only:
// Close drops the token, and nothing about it is ever stored.
type Session struct {
	mu         sync.Mutex
	author     *core.Author
	credential core.Credential
	admitted   bool
}

func NewSession() *Session { return &Session{} }

// Close forgets the connection's identified author and admission.
func (s *Session) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.author, s.credential, s.admitted = nil, core.Credential{}, false
}

func (s *Session) identity() (*core.Author, core.Credential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.author == nil {
		return nil, core.Credential{}, s.admitted
	}
	author := *s.author
	return &author, s.credential, s.admitted
}

func (s *Session) identify(author core.Author, credential core.Credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.author, s.credential = &author, credential
}

func (s *Session) admit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admitted = true
}

// Ingress is how a carrier received a plain request: the client source, the
// connection session (nil for one-shot carriers), and an access password the
// carrier took from outside the body (such as an HTTP header).
type Ingress struct {
	Source   string
	Session  *Session
	Password core.Password
}

// Submit decodes a plain contract request, with its optional author and
// access password, and runs it as that author. Every carrier sends the same
// bytes and gets the same DetachedResult shapes.
func (d *Dispatcher) Submit(ctx context.Context, in Ingress, body []byte, now time.Time) (DetachedResult, error) {
	sub, err := core.DecodeSubmission(body)
	if err != nil {
		return DetachedResult{}, store.ErrInvalid
	}
	return d.SubmitDecoded(ctx, in, sub, now)
}

// SubmitDecoded runs an already decoded submission, for carriers (HTTP GET)
// that build the request from their own parameters.
func (d *Dispatcher) SubmitDecoded(ctx context.Context, in Ingress, sub core.Submission, now time.Time) (DetachedResult, error) {
	author, credential, err := d.resolve(in, sub)
	if err != nil {
		return DetachedResult{}, err
	}
	result, err := d.Admit(ctx, d.listener(), in.Source, author, credential, sub.Request, now)
	if err == nil && sub.Request.Operation == core.AccountIdentify && in.Session != nil {
		in.Session.identify(author, credential)
	}
	return result, err
}

// SubmitWatch is Submit for a streamed watch: every page goes to emit.
func (d *Dispatcher) SubmitWatch(ctx context.Context, in Ingress, body []byte, now time.Time, emit func(backend.HistoryPage) error) error {
	sub, err := core.DecodeSubmission(body)
	if err != nil {
		return store.ErrInvalid
	}
	return d.SubmitDecodedWatch(ctx, in, sub, now, emit)
}

func (d *Dispatcher) SubmitDecodedWatch(ctx context.Context, in Ingress, sub core.Submission, now time.Time, emit func(backend.HistoryPage) error) error {
	author, credential, err := d.resolve(in, sub)
	if err != nil {
		return err
	}
	return d.AdmitWatch(ctx, d.listener(), in.Source, author, credential, sub.Request, now, emit)
}

// resolve applies the listener's access password and picks the author: the
// request's own, else the connection's identified author.
func (d *Dispatcher) resolve(in Ingress, sub core.Submission) (core.Author, core.Credential, error) {
	if d == nil || d.gate == nil {
		return core.Author{}, core.Credential{}, ErrUnsupported
	}
	var sessionAuthor *core.Author
	var sessionCredential core.Credential
	admitted := false
	if in.Session != nil {
		sessionAuthor, sessionCredential, admitted = in.Session.identity()
	}
	if g := d.gate; g.password != nil && !admitted {
		password := sub.Password
		if password.Empty() {
			password = in.Password
		}
		if !password.Matches(*g.password) {
			return core.Author{}, core.Credential{}, auth.ErrForbidden
		}
		if in.Session != nil {
			in.Session.admit()
		}
	}
	if sub.Author != nil {
		return *sub.Author, sub.Credential, nil
	}
	if sessionAuthor != nil {
		return *sessionAuthor, sessionCredential, nil
	}
	return core.Author{}, core.Credential{}, nil
}

// Admit is the single author entry point: it runs r as author, under this
// listener's policy, with AIRC's own semantics. A guest author is an AIRC
// guest under its nickname; a registered author passes AIRC's login with its
// token, which stays in memory. Mutations become durable jobs owned by the
// author scope, so a retry of the same request ID on any carrier is one job;
// reads run as the author (direct-message reads stay blocked); receipt.get
// needs the matching token for a registered author's job and the nickname for
// a guest's; account.register and account.identify are synchronous calls.
// source is the client's address or label, forwarded to AIRC.
func (d *Dispatcher) Admit(ctx context.Context, listener, source string, author core.Author, credential core.Credential, r core.Request, now time.Time) (DetachedResult, error) {
	who, r, err := d.admit(listener, source, author, credential, r, now)
	if err != nil {
		return DetachedResult{}, err
	}
	sub := *who.author
	result := DetachedResult{Version: 1, Operation: r.Operation}
	switch r.Operation {
	case core.CapabilitiesGet:
		result.Capabilities = new(Capabilities)
		*result.Capabilities, err = d.capabilities(ctx, who)
	case core.AccountRegister, core.AccountIdentify:
		var account backend.Account
		if r.Operation == core.AccountRegister {
			account, err = d.backend.Register(ctx, sub.Author, sub.Credential, sub.Source)
		} else {
			account, err = d.backend.Identify(ctx, sub.Author, sub.Credential, sub.Source)
		}
		result.Account = &account
	case core.ReceiptGet:
		var j store.Job
		j, err = d.journal.LookupAuthor(ctx, sub, r.LookupID)
		result.Result = &j.Result
	case core.Directory:
		result.Directory = new(backend.DirectorySnapshot)
		*result.Directory, err = d.directory(ctx, who, r, now)
	case core.ChannelsRead:
		result.Channels = new(core.ChannelsResult)
		*result.Channels, err = d.readChannels(ctx, who, r, now)
	case core.AgentsRead:
		result.Agents = new(core.AgentsResult)
		*result.Agents, err = d.readAgents(ctx, who, r, now)
	case core.ContextRead:
		result.Context = new(core.ContextResult)
		*result.Context, err = d.readContext(ctx, who, r, now)
	case core.PinsRead:
		result.Pins = new(core.PinsResult)
		*result.Pins, err = d.readPins(ctx, who, r, now)
	case core.RoomRead:
		result.Room = new(core.RoomResult)
		*result.Room, err = d.readRoom(ctx, who, r, now)
	case core.WaitingRead:
		result.Waiting = new(core.WaitingResult)
		*result.Waiting, err = d.readWaiting(ctx, who, r, now)
	case core.PollResults:
		result.Poll = new(core.PollResult)
		*result.Poll, err = d.readPoll(ctx, who, r, now)
	case core.TopicRead:
		result.Topic = new(core.TopicResult)
		*result.Topic, err = d.readTopic(ctx, who, r, now)
	case core.MembersRead:
		result.Members = new(core.MembersResult)
		*result.Members, err = d.readMembers(ctx, who, r, now)
	case core.HistoryRead, core.Search, core.Watch, core.EventsWatch:
		result.Page = new(backend.HistoryPage)
		*result.Page, err = d.readPage(ctx, who, r, "", now, nil)
	default:
		if !r.Operation.IsMutation() {
			return DetachedResult{}, ErrUnsupported
		}
		var j store.Job
		j, _, err = d.journal.AdmitAuthor(ctx, sub, r, 0, now)
		result.Result = &j.Result
	}
	if err != nil {
		return DetachedResult{}, err
	}
	return result, nil
}

// AdmitWatch is Admit for a streamed watch: each page goes to emit until
// the read lease ends, which the carrier reports as a renewable end.
func (d *Dispatcher) AdmitWatch(ctx context.Context, listener, source string, author core.Author, credential core.Credential, r core.Request, now time.Time, emit func(backend.HistoryPage) error) error {
	who, r, err := d.admit(listener, source, author, credential, r, now)
	if err != nil {
		return err
	}
	if !r.Operation.IsWatch() || emit == nil {
		return store.ErrInvalid
	}
	_, err = d.readPage(ctx, who, r, "", now, emit)
	return err
}

// admit applies the listener policy and resolves the actor. The gateway is
// not an identity authority: it checks only shape and policy here, and AIRC
// decides whether the author may be who it says.
func (d *Dispatcher) admit(listener, source string, author core.Author, credential core.Credential, r core.Request, now time.Time) (actor, core.Request, error) {
	if d == nil || d.gate == nil || d.backend == nil {
		return actor{}, r, ErrUnsupported
	}
	g := d.gate
	if r.Target != nil {
		target := *r.Target
		r.Target = &target
	}
	if err := r.Normalize(); err != nil {
		return actor{}, r, store.ErrInvalid
	}
	op := r.Operation
	if !slices.Contains(g.operations, op) {
		return actor{}, r, ErrUnsupported
	}
	if r.Target != nil && g.targets != nil && !slices.Contains(g.targets, *r.Target) {
		return actor{}, r, auth.ErrForbidden
	}
	anonymous := author == core.Author{}
	if anonymous && op != core.CapabilitiesGet || !anonymous && (!author.Valid() || author.Registered == credential.Empty()) {
		return actor{}, r, store.ErrInvalid
	}
	if op == core.AccountRegister && !author.Registered {
		return actor{}, r, store.ErrInvalid
	}
	if !anonymous && !author.Registered && !g.guests {
		return actor{}, r, auth.ErrForbidden
	}
	if source == "" {
		source = store.ListenerSource(listener)
	}
	if !protocol.ValidSource(source) || len(listener) > 64 {
		return actor{}, r, store.ErrInvalid
	}
	// As in aircd, loopback clients are not limited per source; every author
	// still is.
	if !loopbackSource(source) && !g.perSource.allow(source, now) || !anonymous && !g.perAuthor.allow(author.Scope(), now) {
		return actor{}, r, store.ErrCapacity
	}
	return actor{author: &store.Submission{Author: author, Credential: credential, Listener: listener, Source: source}, gate: g}, r, nil
}

// loopbackSource reports a source that is a loopback IP address. Labels such
// as listener:, route: and bridge: sources are never exempt.
func loopbackSource(source string) bool {
	ip := net.ParseIP(source)
	return ip != nil && ip.IsLoopback()
}
