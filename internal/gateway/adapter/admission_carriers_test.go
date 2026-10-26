//go:build darwin || linux

package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/mqttwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestCarrierAdmission exercises the admission every carrier that carries
// plain contract JSON shares, once per carrier: requests post as the author
// they name, replay one durable job across a journal reopen, refuse before
// intake without touching the journal or another request's slot, and bound a
// native read by the admission deadline. Wire framing and carrier metadata
// stay in each carrier's own tests.
func TestCarrierAdmission(t *testing.T) {
	for _, c := range admissionCarriers() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel() // Each carrier has its own journal and sockets.
			t.Run("author", func(t *testing.T) { testAdmissionAuthor(t, c) })
			t.Run("replay", func(t *testing.T) { testAdmissionReplay(t, c) })
			// Refusals leave the journal untouched, so they share one.
			r := newAdmissionRig(t, c)
			for _, kind := range []string{"body-target", "body-operation", "anonymous", "malformed", "unknown-field", "author-type", "compact-token", "capacity", "cancelled"} {
				if kind == "capacity" && c.noSlots {
					continue
				}
				t.Run("refusal/"+kind, func(t *testing.T) { testAdmissionRefusal(t, r, kind) })
			}
			if c.reads {
				for _, op := range []core.Operation{core.ContextRead, core.HistoryRead, core.Search} {
					t.Run("bounded-read/"+string(op), func(t *testing.T) { testAdmissionBoundedRead(t, c, op) })
				}
			}
		})
	}
}

// admissionOutcome is what one carrier admission tells its client.
type admissionOutcome struct {
	admitted  bool          // durable intake (or a read result) reported
	refusal   string        // refused before a request identity exists
	failure   *core.Failure // correlated failure
	requestID string
	resultID  string // the durable result's own request ID
}

// admissionCarrier admits one plain contract request body over a carrier.
type admissionCarrier struct {
	name string
	// reports: the carrier tells its client whether intake happened.
	reports bool
	// correlated: failures carry a code and the request's own ID.
	correlated bool
	// answers: failures carry a code but name no request.
	answers bool
	// codes overrides a refusal kind's expected code where the carrier
	// answers.
	codes map[string]core.ErrorCode
	// decodeRefusal is the uncorrelated refusal of a body that is not a
	// contract request; empty when the carrier does not report it.
	decodeRefusal string
	// waitsForSlot: a full request slot is awaited until the context ends.
	waitsForSlot bool
	// reads: the carrier admits context, history and search reads.
	reads bool
	// noSlots: the carrier takes no request slot.
	noSlots bool
	// dispatcher builds the carrier's dispatcher over f's journal.
	dispatcher func(t *testing.T, f *routeFixture) *dispatch.Dispatcher
	// admit carries body; attempt varies wire correlation, which never
	// drives replay.
	admit func(t *testing.T, ctx context.Context, r *admissionRig, body []byte, slots chan struct{}, attempt int) admissionOutcome
}

// admissionRig is one carrier's dispatcher over a real journal.
type admissionRig struct {
	*routeFixture
	carrier admissionCarrier
	d       *dispatch.Dispatcher
	in      dispatch.Ingress
}

func newAdmissionRig(t *testing.T, c admissionCarrier) *admissionRig {
	t.Helper()
	f := newRouteFixture(t)
	return &admissionRig{routeFixture: f, carrier: c, d: c.dispatcher(t, f), in: dispatch.Ingress{Source: "192.0.2.1", Session: dispatch.NewSession()}}
}

func (r *admissionRig) admit(t *testing.T, ctx context.Context, body []byte, slots chan struct{}, attempt int) admissionOutcome {
	t.Helper()
	return r.carrier.admit(t, ctx, r, body, slots, attempt)
}

// reopen closes and reopens the journal under a fresh dispatcher.
func (r *admissionRig) reopen(t *testing.T) {
	t.Helper()
	if err := r.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), r.c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r.s = s
	r.d = r.carrier.dispatcher(t, r.routeFixture)
}

// job reads requestID's job as nick's.
func (r *admissionRig) job(t *testing.T, nick string, registered bool, requestID string) (store.Job, error) {
	t.Helper()
	sub := store.Submission{Author: core.Author{Nick: nick, Registered: registered}}
	if registered {
		sub.Credential, _ = core.NewCredential(authorTestToken)
	}
	return r.s.LookupAuthor(t.Context(), sub, requestID)
}

const admissionGuest = "carrier-guest"

func admissionRequest() core.Request {
	return core.Request{Version: 1, RequestID: "carrier-request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "exact body\n世界"}
}

func guestBody(t *testing.T, r core.Request) []byte {
	t.Helper()
	return requestJSON(t, r, map[string]string{"nick": admissionGuest})
}

func testAdmissionAuthor(t *testing.T, c admissionCarrier) {
	r := newAdmissionRig(t, c)
	request := admissionRequest()
	slots := make(chan struct{}, 1)
	got := r.admit(t, t.Context(), guestBody(t, request), slots, 0)
	if c.reports && (!got.admitted || got.failure != nil || got.refusal != "") || c.correlated && (got.requestID != request.RequestID || got.resultID != request.RequestID) || len(slots) != 0 {
		t.Fatalf("guest admission: %+v %+v", got, got.failure)
	}
	job, err := r.job(t, admissionGuest, false, request.RequestID)
	if err != nil || job.Author != (core.Author{Nick: admissionGuest}) || job.Request.Operation != core.Send || job.Request.Target == nil || *job.Request.Target != *request.Target || job.Request.Message != request.Message || !job.Result.Durable {
		t.Fatalf("journal attribution: %+v %v", job, err)
	}
	// A registered author's token is hashed into the journal, never trusted
	// from the carrier.
	registered := request
	registered.RequestID = "registered-request"
	got = r.admit(t, t.Context(), requestJSON(t, registered, map[string]string{"nick": "carol", "token": authorTestToken}), slots, 1)
	if c.reports && (!got.admitted || got.failure != nil) || len(slots) != 0 {
		t.Fatalf("registered admission: %+v %+v", got, got.failure)
	}
	if a := r.owner(t, "carol", true, registered.RequestID); a != (core.Author{Nick: "carol", Registered: true}) {
		t.Fatal("wrong registered author", a)
	}
}

func testAdmissionReplay(t *testing.T, c admissionCarrier) {
	r := newAdmissionRig(t, c)
	request := admissionRequest()
	body := guestBody(t, request)
	slots := make(chan struct{}, 1)
	var upstream string
	for i := range 3 {
		got := r.admit(t, t.Context(), body, slots, i)
		if c.reports && (!got.admitted || got.failure != nil) || c.correlated && got.requestID != request.RequestID {
			t.Fatalf("durable admission %d: %+v %+v", i, got, got.failure)
		}
		job, err := r.job(t, admissionGuest, false, request.RequestID)
		if err != nil || job.Request.Message != request.Message || !job.Result.Durable || job.Result.SendAttempts != 0 || upstream != "" && upstream != job.UpstreamID {
			t.Fatal("journal intent changed", job, err)
		}
		upstream = job.UpstreamID
		if r.jobs(t, request.RequestID) != 1 || len(slots) != 0 {
			t.Fatal("replay duplicated intake or leaked capacity")
		}
		if i == 0 {
			r.reopen(t)
		}
	}
	changed := request
	changed.Message = "changed intent"
	got := r.admit(t, t.Context(), guestBody(t, changed), slots, 3)
	if c.reports && got.admitted || (c.correlated || c.answers) && (got.failure == nil || got.failure.Code != core.IDConflict || got.failure.Transient) || len(slots) != 0 {
		t.Fatalf("conflict: %+v %+v", got, got.failure)
	}
	job, err := r.job(t, admissionGuest, false, request.RequestID)
	if err != nil || job.UpstreamID != upstream || job.Request.Message != request.Message {
		t.Fatal("conflict changed the original durable intent", job, err)
	}
}

func testAdmissionRefusal(t *testing.T, r *admissionRig, kind string) {
	c := r.carrier
	request := admissionRequest()
	body := guestBody(t, request)
	slots := make(chan struct{}, 1)
	ctx := t.Context()
	switch kind {
	case "body-target":
		// Every listener here is fixed to #fixture.
		other := request
		other.Target = &core.Target{Kind: "room", Name: "#other"}
		body = guestBody(t, other)
	case "body-operation":
		body = guestBody(t, core.Request{Version: 1, RequestID: request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID})
	case "anonymous":
		body = requestJSON(t, request, nil)
	case "malformed":
		body = []byte(`{"version":1,"request_id":`)
	case "unknown-field":
		body = []byte(`{"version":1,"request_id":"carrier-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x","principal":"other"}`)
	case "author-type":
		body = []byte(`{"version":1,"request_id":"carrier-request","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x","author":"other"}`)
	case "compact-token":
		// A compact JWS is not a contract request and is not verified here.
		body = []byte("eyJhbGciOiJFZERTQSJ9.eyJ2ZXJzaW9uIjoxfQ.c2lnbmF0dXJl")
	case "capacity":
		slots <- struct{}{}
		if c.waitsForSlot {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
		}
	case "cancelled":
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	got := r.admit(t, ctx, body, slots, 0)
	if c.reports && got.admitted || (c.correlated || c.answers) && got.refusal == "" && got.failure == nil {
		t.Fatalf("accepted refusal: %+v", got)
	}
	var want admissionOutcome
	switch kind {
	case "body-target", "body-operation":
		want = admissionOutcome{failure: &core.Failure{Code: core.Forbidden}, requestID: request.RequestID}
	case "anonymous":
		want = admissionOutcome{failure: &core.Failure{Code: core.InvalidRequest}, requestID: request.RequestID}
	case "malformed", "unknown-field", "author-type", "compact-token":
		want = admissionOutcome{refusal: c.decodeRefusal}
	case "capacity":
		want = admissionOutcome{failure: &core.Failure{Code: core.CapacityExceeded, Transient: true}, requestID: request.RequestID}
	case "cancelled":
		want = admissionOutcome{failure: &core.Failure{Transient: true}, requestID: request.RequestID}
	}
	if code, ok := c.codes[kind]; ok {
		want = admissionOutcome{failure: &core.Failure{Code: code}}
	}
	if c.answers {
		// Only the code and its transience reach the peer.
		want.requestID, got.requestID = "", ""
	}
	if (c.correlated || c.answers) && (got.refusal != want.refusal || got.requestID != want.requestID || (got.failure == nil) != (want.failure == nil) ||
		want.failure != nil && (got.failure.Transient != want.failure.Transient || want.failure.Code != "" && got.failure.Code != want.failure.Code)) {
		t.Fatalf("got %+v %+v, want %+v %+v", got, got.failure, want, want.failure)
	}
	wantSlots := 0
	if kind == "capacity" {
		wantSlots = 1
	}
	if len(slots) != wantSlots {
		t.Fatal("refusal changed another request's slot")
	}
	if r.jobs(t, request.RequestID) != 0 {
		t.Fatal("refusal reached intake")
	}
}

// testAdmissionBoundedRead: a read whose native AIRC session never answers
// fails transiently by the admission deadline and closes that socket.
func testAdmissionBoundedRead(t *testing.T, c admissionCarrier, op core.Operation) {
	r := newAdmissionRig(t, c)
	assertNativeClosed := stalledNativeRead(t, r.c.Upstream.Address)
	request := core.Request{Version: 1, RequestID: "carrier-read", Operation: op, Target: admissionRequest().Target, Limit: 1}
	if op == core.ContextRead {
		request.ParentID = strings.Repeat("a", 32)
	}
	if op == core.Search {
		request.Query = "exact"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	slots := make(chan struct{}, 1)
	got := r.admit(t, ctx, guestBody(t, request), slots, 0)
	if got.admitted || got.refusal != "" || got.failure == nil || !got.failure.Transient || got.requestID != request.RequestID || time.Since(started) > time.Second || len(slots) != 0 {
		t.Fatalf("bounded read attempt: %+v %v", got, time.Since(started))
	}
	assertNativeClosed()
	if r.jobs(t, request.RequestID) != 0 {
		t.Fatal("read admitted mutation")
	}
}

func admissionCarriers() []admissionCarrier {
	listener := func(l config.Listener) func(*testing.T, *routeFixture) *dispatch.Dispatcher {
		return func(t *testing.T, f *routeFixture) *dispatch.Dispatcher { return f.listener(t, l) }
	}
	return []admissionCarrier{
		{
			name: "mqtt", reports: true,
			dispatcher: listener(mqttFixtureListener()),
			admit: func(t *testing.T, ctx context.Context, r *admissionRig, body []byte, slots chan struct{}, attempt int) admissionOutcome {
				// QoS 1, its duplicate and a QoS 0 resend are one durable job.
				packet := mqttwire.Packet{Kind: mqttwire.Publish, QoS: 1, ID: uint16(7 + attempt), Duplicate: attempt == 1, Topic: "airc/send", Payload: string(body)}
				if attempt == 2 {
					packet.QoS, packet.ID = 0, 0
				}
				return admissionOutcome{admitted: admitMQTT(ctx, packet, r.in, mqttFixtureRoutes(), r.d, slots)}
			},
		},
	}
}
