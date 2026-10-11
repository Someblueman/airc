//go:build darwin || linux

package adapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestRouteAdmission runs the author scenarios of route-owned ingress that
// carries contract-v1 requests (Redis Pub/Sub and Streams, PostgreSQL
// LISTEN/NOTIFY, the SFTP mailbox and the SCP inbox) through each carrier's
// admission path against a real journal: who a request posts as, which
// requests are refused before intake, and that a redelivery is one job. The
// brokers themselves (scripted peers) are covered by each carrier's own
// tests; TestRouteSuite in integration/gateway runs the same scenarios end to
// end for carriers whose peers send provider records.
func TestRouteAdmission(t *testing.T) {
	for _, c := range routeIngresses {
		t.Run(c.name, func(t *testing.T) {
			e := &admissionEnv{carrier: c, f: newRouteFixture(t)}
			t.Cleanup(func() { _ = e.f.s.Close() }) // the journal reopen replaced
			e.d = e.f.route(t, c.name, core.Send)
			e.guests = e.f.author(t, "", false, true)
			e.bound = e.f.author(t, "alerts", true, false)
			for _, s := range admissionScenarios {
				if s.applies == nil || s.applies(c) {
					t.Run(s.name, func(t *testing.T) { s.run(t, e) })
				}
			}
		})
	}
}

// routeIngress is one carrier's admission path.
type routeIngress struct {
	name string
	// admit runs one payload as the carrier's owner does; delivery numbers
	// redeliveries of the same payload, which a broker may make on another
	// channel or under another entry ID.
	admit func(*testing.T, context.Context, *dispatch.Dispatcher, dispatch.RouteAuthor, []byte, chan struct{}, int) routeOutcome
	// answers: the carrier reports refusals to the peer, with a code;
	// correlates: it names the refused request's ID.
	answers, correlates bool
	// codes overrides a refusal scenario's expected code.
	codes map[string]core.ErrorCode
	slots bool // admission takes a shared request slot
}

// routeOutcome is the outcome of one admission.
type routeOutcome struct {
	admitted  bool
	code      core.ErrorCode
	transient bool
	requestID string
}

var admissionTarget = core.Target{Kind: "room", Name: "#fixture"}

var routeIngresses = []routeIngress{
	{name: "redis_pubsub", answers: true, correlates: true, slots: true, codes: map[string]core.ErrorCode{"malformed": "", "unknown_member": ""},
		admit: func(t *testing.T, ctx context.Context, d *dispatch.Dispatcher, a dispatch.RouteAuthor, payload []byte, slots chan struct{}, delivery int) routeOutcome {
			route := config.RedisRoute{RequestChannel: "airc request.*é", ResponseChannel: "airc response", Target: admissionTarget, Operations: []core.Operation{core.Send}}
			if delivery == 2 {
				route.RequestChannel, route.ResponseChannel = "other request", "other response"
			}
			return redisOutcome(admitRedis(ctx, d, a, "route:redis", route, route.RequestChannel, payload, slots))
		}},
	{name: "redis_streams", answers: true, correlates: true, slots: true, codes: map[string]core.ErrorCode{"malformed": "", "unknown_member": ""},
		admit: func(t *testing.T, ctx context.Context, d *dispatch.Dispatcher, a dispatch.RouteAuthor, payload []byte, slots chan struct{}, delivery int) routeOutcome {
			broker := config.RedisStreams{ID: "streams", Namespace: "broker", Address: "127.0.0.1:6379", Group: "gateway", Consumer: "owner",
				Route: config.RedisStreamRoute{Stream: "events", ResultStream: "results", Target: admissionTarget, Operations: []core.Operation{core.Send}}}
			if delivery == 2 {
				broker.ID, broker.Group, broker.Consumer, broker.Address = "restarted", "another-group", "another-consumer", "127.0.0.1:6380"
			}
			entry := rediswire.StreamEntry{ID: fmt.Sprintf("1712345678901-%d", delivery), Payload: payload}
			return redisOutcome(admitRedisStream(ctx, d, a, broker, broker.Route.Stream, entry, slots))
		}},
	{name: "postgres_notify", slots: true,
		admit: func(t *testing.T, ctx context.Context, d *dispatch.Dispatcher, a dispatch.RouteAuthor, payload []byte, slots chan struct{}, _ int) routeOutcome {
			if err := admitPostgresNotify(ctx, d, a, "route:pg", config.PostgresNotifyRoute{Target: admissionTarget}, payload, slots); err != nil {
				t.Fatal("refusal ended the owner", err)
			}
			return routeOutcome{}
		}},
	{name: "sftp", answers: true, correlates: true, codes: map[string]core.ErrorCode{"malformed": core.InvalidRequest},
		admit: func(t *testing.T, ctx context.Context, d *dispatch.Dispatcher, a dispatch.RouteAuthor, payload []byte, _ chan struct{}, _ int) routeOutcome {
			got := admitSFTP(ctx, d, a, config.SFTP{ID: "sftp", Route: config.SFTPRoute{Target: admissionTarget, Operations: []core.Operation{core.Send}}}, payload)
			return routeOutcomeOf(got.Result, got.Error, got.RequestID)
		}},
	{name: "scp_inbox", answers: true, slots: true, codes: map[string]core.ErrorCode{"target": core.InvalidRequest, "operation": core.InvalidRequest, "malformed": core.InvalidRequest, "unknown_member": core.InvalidRequest},
		admit: func(t *testing.T, ctx context.Context, d *dispatch.Dispatcher, a dispatch.RouteAuthor, payload []byte, slots chan struct{}, _ int) routeOutcome {
			if err := admitSCPInbox(ctx, d, config.SCPInbox{Target: admissionTarget}, a, payload, slots); err != nil {
				failure := errorFailureOf(err)
				return routeOutcomeOf(nil, &failure, "")
			}
			return routeOutcome{admitted: true}
		}},
}

func redisOutcome(a redisAdmission) routeOutcome {
	return routeOutcomeOf(a.result, a.failure, a.requestID)
}

func routeOutcomeOf(result *dispatch.DetachedResult, failure *core.Failure, requestID string) routeOutcome {
	o := routeOutcome{admitted: result != nil && result.Result != nil && result.Result.Durable, requestID: requestID}
	if failure != nil {
		o.code, o.transient = failure.Code, failure.Transient
	}
	return o
}

// admissionEnv is one carrier's journal and route dispatcher, with a guest
// route policy and a route bound to the registered alerts.
type admissionEnv struct {
	carrier       routeIngress
	f             *routeFixture
	d             *dispatch.Dispatcher
	guests, bound dispatch.RouteAuthor
	sequence      int
}

// id is a request ID unique to the environment.
func (e *admissionEnv) id(name string) string {
	e.sequence++
	return fmt.Sprintf("%s-%d", name, e.sequence)
}

// admit runs payload (which the carrier may consume) and checks that a
// request slot is never kept.
func (e *admissionEnv) admit(t *testing.T, ctx context.Context, a dispatch.RouteAuthor, payload []byte, delivery int) routeOutcome {
	t.Helper()
	slots := make(chan struct{}, 1)
	o := e.carrier.admit(t, ctx, e.d, a, bytes.Clone(payload), slots, delivery)
	if len(slots) != 0 {
		t.Fatal("request slot kept")
	}
	return o
}

// routeSendPayload is a contract-v1 send of a whole two-line body; author is the
// author member's JSON, if any.
func routeSendPayload(id, room, message, author string) []byte {
	body := fmt.Sprintf(`{"version":1,"request_id":%q,"operation":"send","target":{"kind":"room","name":%q},"message":%q`, id, room, message)
	if author != "" {
		body += `,"author":` + author
	}
	return []byte(body + "}")
}

// posts admits a send and checks the job is the whole request, by author.
func (e *admissionEnv) posts(t *testing.T, a dispatch.RouteAuthor, author, nick string, registered bool) {
	t.Helper()
	id, message := e.id("post"), "first\nsecond 世界 🙂"
	if o := e.admit(t, t.Context(), a, routeSendPayload(id, admissionTarget.Name, message, author), 0); e.carrier.answers && (!o.admitted || e.carrier.correlates && o.requestID != id) {
		t.Fatalf("not admitted: %+v", o)
	}
	job := e.job(t, nick, registered, id)
	if job.Author != (core.Author{Nick: nick, Registered: registered}) || job.Request.Message != message || job.Request.Target == nil || *job.Request.Target != admissionTarget || !job.Result.Durable {
		t.Fatalf("job %+v by %+v", job.Request, job.Author)
	}
}

func (e *admissionEnv) job(t *testing.T, nick string, registered bool, id string) store.Job {
	t.Helper()
	sub := store.Submission{Author: core.Author{Nick: nick, Registered: registered}}
	if registered {
		sub.Credential, _ = core.NewCredential(authorTestToken)
	}
	job, err := e.f.s.LookupAuthor(t.Context(), sub, id)
	if err != nil {
		t.Fatalf("job %s by %s: %v", id, nick, err)
	}
	return job
}

// refused checks that a request was refused before intake. Where the carrier
// answers, the code is code (or the carrier's override), transient only for
// capacity, and names the request when it could be decoded.
func (e *admissionEnv) refused(t *testing.T, o routeOutcome, scenario, id string, code core.ErrorCode, decodable bool) {
	t.Helper()
	if override, ok := e.carrier.codes[scenario]; ok {
		code = override
	}
	want := ""
	if decodable && code != "" {
		want = id
	}
	if o.admitted || e.carrier.answers && (o.code != code || o.transient != (code == core.CapacityExceeded)) || e.carrier.correlates && o.requestID != want {
		t.Fatalf("want a %q refusal of %s: %+v", code, id, o)
	}
	if n := e.f.jobs(t, id); n != 0 {
		t.Fatalf("refused request %s reached intake (%d jobs)", id, n)
	}
}

var admissionScenarios = []struct {
	name    string
	applies func(routeIngress) bool
	run     func(*testing.T, *admissionEnv)
}{
	{"guest_post", nil, admitGuestPost},
	{"bound_post", nil, admitBoundPost},
	{"registered_body_author", nil, admitRegisteredBodyAuthor},
	{"refusals", nil, admitRefusals},
	{"replay_one_job", nil, admitReplayOneJob},
	{"conflicting_retry", nil, admitConflictingRetry},
	{"capacity", func(c routeIngress) bool { return c.slots }, admitCapacity},
}

// A guest route posts the nickname a request names as a guest.
func admitGuestPost(t *testing.T, e *admissionEnv) {
	e.posts(t, e.guests, `{"nick":"route-guest"}`, "route-guest", false)
}

// A request without an author posts as the route's bound author, registered
// or not.
func admitBoundPost(t *testing.T, e *admissionEnv) {
	e.posts(t, e.bound, "", "alerts", true)
	e.posts(t, e.f.author(t, "bot", false, false), "", "bot", false)
}

// A bound route admits a registered author named with its token: AIRC, not
// the route, decides whether it may post.
func admitRegisteredBodyAuthor(t *testing.T, e *admissionEnv) {
	e.posts(t, e.bound, `{"nick":"carol","token":"`+authorTestToken+`"}`, "carol", true)
}

// Requests the route or its author policy does not admit never reach intake.
func admitRefusals(t *testing.T, e *admissionEnv) {
	for _, c := range []struct {
		name      string
		author    dispatch.RouteAuthor
		body      func(id string) []byte
		code      core.ErrorCode
		decodable bool
	}{
		{"guest_on_bound", e.bound, func(id string) []byte { return routeSendPayload(id, "#fixture", "hi", `{"nick":"mallory"}`) }, core.Forbidden, true},
		{"anonymous_on_guest_route", e.guests, func(id string) []byte { return routeSendPayload(id, "#fixture", "hi", "") }, core.InvalidRequest, true},
		{"target", e.guests, func(id string) []byte { return routeSendPayload(id, "#other", "hi", `{"nick":"route-guest"}`) }, core.Forbidden, true},
		{"operation", e.guests, func(id string) []byte {
			return fmt.Appendf(nil, `{"version":1,"request_id":%q,"operation":"receipt.get","lookup_id":"missing","author":{"nick":"route-guest"}}`, id)
		}, core.Forbidden, true},
		{"malformed", e.guests, func(id string) []byte {
			return bytes.TrimSuffix(routeSendPayload(id, "#fixture", "hi", `{"nick":"route-guest"}`), []byte("}"))
		}, core.InvalidRequest, false},
		{"unknown_member", e.guests, func(id string) []byte { return routeSendPayload(id, "#fixture", "hi", `"other"`) }, core.InvalidRequest, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			id := e.id(c.name)
			e.refused(t, e.admit(t, t.Context(), c.author, c.body(id), 0), c.name, id, c.code, c.decodable)
		})
	}
}

// Redelivery of one request, after the journal reopens and on another
// channel, entry ID or consumer where the broker has them, is one job.
func admitReplayOneJob(t *testing.T, e *admissionEnv) {
	id := e.id("replay")
	payload := routeSendPayload(id, "#fixture", "replayed", `{"nick":"route-guest"}`)
	upstream := ""
	for delivery := range 3 {
		if o := e.admit(t, t.Context(), e.guests, payload, delivery); e.carrier.answers && (!o.admitted || e.carrier.correlates && o.requestID != id) {
			t.Fatalf("delivery %d: %+v", delivery, o)
		}
		job := e.job(t, "route-guest", false, id)
		if job.Request.Message != "replayed" || job.Result.SendAttempts != 0 || upstream != "" && job.UpstreamID != upstream {
			t.Fatalf("delivery %d changed the job: %+v", delivery, job)
		}
		upstream = job.UpstreamID
		if delivery == 0 {
			e.reopen(t)
		}
	}
	if n := e.f.jobs(t, id); n != 1 {
		t.Fatalf("%d jobs for one request", n)
	}
}

// A request ID names one intent: a retry with another body is refused and
// the journal keeps the original.
func admitConflictingRetry(t *testing.T, e *admissionEnv) {
	id := e.id("conflict")
	e.admit(t, t.Context(), e.guests, routeSendPayload(id, "#fixture", "original", `{"nick":"route-guest"}`), 0)
	o := e.admit(t, t.Context(), e.guests, routeSendPayload(id, "#fixture", "changed", `{"nick":"route-guest"}`), 0)
	if o.admitted || e.carrier.answers && (o.code != core.IDConflict || o.transient) {
		t.Fatalf("conflicting retry: %+v", o)
	}
	if job := e.job(t, "route-guest", false, id); job.Request.Message != "original" {
		t.Fatal("a conflicting retry changed the job", job.Request)
	}
}

// Without a free request slot nothing is admitted and another owner's slot is
// left alone.
func admitCapacity(t *testing.T, e *admissionEnv) {
	id := e.id("capacity")
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o := e.carrier.admit(t, ctx, e.d, e.guests, routeSendPayload(id, "#fixture", "no slot", `{"nick":"route-guest"}`), slots, 0)
	if len(slots) != 1 {
		t.Fatal("another owner's slot was taken or released")
	}
	e.refused(t, o, "capacity", id, core.CapacityExceeded, true)
}

// reopen closes the journal and serves the route from a reopened one.
func (e *admissionEnv) reopen(t *testing.T) {
	t.Helper()
	if err := e.f.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), e.f.c)
	if err != nil {
		t.Fatal(err)
	}
	e.f.s = s
	e.d = e.f.route(t, e.carrier.name, core.Send)
}

// A closed journal is the one PostgreSQL admission error that ends the owner.
func TestPostgresNotifyClosedJournalEndsOwner(t *testing.T) {
	f := newRouteFixture(t)
	d := f.route(t, "pg", core.Send)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	err := admitPostgresNotify(t.Context(), d, f.author(t, "", false, true), "route:pg", config.PostgresNotifyRoute{Target: admissionTarget}, []byte(plainRouteSend), make(chan struct{}, 1))
	if !errors.Is(err, store.ErrClosed) {
		t.Fatal("closed journal must end the owner", err)
	}
}
