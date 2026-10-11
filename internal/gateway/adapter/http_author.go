package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// Plain author requests carry the contract's "author" (and "password") in the
// JSON body. GET requests have no body, so they name the author with these
// headers instead; a body's own author wins over them.
const (
	headerNick     = "AIRC-Nick"
	headerToken    = "AIRC-Token"
	headerPassword = "AIRC-Password"
)

// httpAuthor serves a plain contract request run as its author. Like IRC, it
// needs no TLS; TLS stays an option, and account tokens sent without it can
// be observed on the network.
func httpAuthor(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, readPage bool) {
	in := dispatch.Ingress{Source: d.RemoteSource(r.RemoteAddr)}
	author, err := headerSubmission(r, &in)
	if err != nil {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	switch {
	case r.Method == http.MethodPost && (r.URL.Path == "/v1/rpc" || r.URL.Path == "/v1/rpc/events"):
		caller := authorRPC(d, in)
		if r.URL.Path == "/v1/rpc/events" {
			httpJSONRPCEvents(w, r, caller)
		} else {
			httpJSONRPC(w, r, caller)
		}
	case r.Method == http.MethodPost && (r.URL.Path == "/v1/request" || r.URL.Path == "/v1/messages") || r.Method == http.MethodPut && requestResourceID(r.URL.Path) != "":
		httpAuthorBody(w, r, d, in, author)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/requests/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/requests/")
		author.Request = core.Request{Version: 1, RequestID: "status", Operation: core.ReceiptGet, LookupID: id}
		httpAuthorCall(w, r, d, in, author, false)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/capabilities":
		author.Request = core.Request{Version: 1, RequestID: "capabilities", Operation: core.CapabilitiesGet}
		httpAuthorCall(w, r, d, in, author, false)
	case readPage:
		request, discovery, err := discoveryRequest(r)
		start := ""
		if discovery {
			author.Request = request
		} else {
			var body []byte
			body, start, err = readRequest(r)
			if err == nil {
				author.Request, err = core.Decode(body)
			}
		}
		if err == nil && start != "" {
			author.Request.Start = start
			err = author.Request.Normalize()
		}
		if err != nil {
			httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
			return
		}
		if r.URL.Path == "/v1/events" || r.URL.Path == "/v1/live/events" {
			httpAuthorEvents(w, r, d, in, author)
			return
		}
		httpAuthorCall(w, r, d, in, author, false)
	default:
		httpFailure(w, http.StatusNotFound, core.InvalidRequest)
	}
}

// headerSubmission reads the header author and password. Each header may
// appear at most once.
func headerSubmission(r *http.Request, in *dispatch.Ingress) (core.Submission, error) {
	var sub core.Submission
	for _, name := range []string{headerNick, headerToken, headerPassword} {
		if len(r.Header.Values(name)) > 1 {
			return sub, store.ErrInvalid
		}
	}
	if nick := r.Header.Get(headerNick); nick != "" {
		author, credential, err := core.NewAuthor(nick, r.Header.Get(headerToken))
		if err != nil {
			return sub, err
		}
		sub.Author, sub.Credential = &author, credential
	} else if r.Header.Get(headerToken) != "" {
		return sub, store.ErrInvalid
	}
	if value := r.Header.Get(headerPassword); value != "" {
		password, err := core.NewPassword(value)
		if err != nil {
			return sub, err
		}
		in.Password = password
	}
	return sub, nil
}

// httpAuthorBody accepts a JSON contract request: any operation on
// POST /v1/request, mutations on POST /v1/messages and PUT /v1/requests/{id}.
func httpAuthorBody(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, in dispatch.Ingress, headers core.Submission) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		httpFailure(w, http.StatusUnsupportedMediaType, core.InvalidRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, core.MaxRequestBytes))
	if err != nil {
		var oversized *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &oversized) {
			status = http.StatusRequestEntityTooLarge
		}
		httpFailure(w, status, core.InvalidRequest)
		return
	}
	sub, err := core.DecodeSubmission(body)
	if err != nil {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	putID := requestResourceID(r.URL.Path)
	keys := r.Header.Values("Idempotency-Key")
	if r.Method == http.MethodPut && sub.Request.RequestID != putID || len(keys) != 0 && (len(keys) != 1 || keys[0] != sub.Request.RequestID) {
		httpFailure(w, http.StatusConflict, core.IDConflict)
		return
	}
	if r.URL.Path != "/v1/request" && !sub.Request.Operation.IsMutation() {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	if sub.Author == nil {
		sub.Author, sub.Credential = headers.Author, headers.Credential
	}
	httpAuthorCall(w, r, d, in, sub, r.URL.Path == "/v1/request")
}

// httpAuthorCall runs one submission. POST /v1/request answers with the
// DetachedResult every carrier returns; the resource routes keep their
// established bodies (a Result, a page, a capabilities document...).
func httpAuthorCall(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, in dispatch.Ingress, sub core.Submission, detached bool) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	result, err := d.SubmitDecoded(ctx, in, sub, time.Now())
	if err != nil {
		httpFailureErr(w, err)
		return
	}
	status := http.StatusOK
	if sub.Request.Operation.IsMutation() {
		status = http.StatusAccepted
		w.Header().Set("Location", "/v1/requests/"+sub.Request.RequestID)
	}
	w.WriteHeader(status)
	if detached {
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	_ = json.NewEncoder(w).Encode(detachedValue(result))
}

// detachedValue is the single operation-specific value of a result.
func detachedValue(result dispatch.DetachedResult) any {
	for _, value := range []any{result.Result, result.Page, result.Directory, result.Channels, result.Agents, result.Context, result.Pins, result.Room, result.Waiting, result.Poll, result.Topic, result.Members, result.Capabilities, result.Account} {
		if !reflect.ValueOf(value).IsNil() {
			return value
		}
	}
	return result
}

// httpAuthorEvents streams a watch as server-sent events, resuming from
// Last-Event-ID.
func httpAuthorEvents(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, in dispatch.Ingress, sub core.Submission) {
	if ids := r.Header.Values("Last-Event-ID"); len(ids) != 0 {
		if len(ids) != 1 || len(ids[0]) > 256 || ids[0] == "" || sub.Request.Cursor != "" && sub.Request.Cursor != ids[0] {
			httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
			return
		}
		sub.Request.Cursor, sub.Request.Start = ids[0], ""
	}
	httpEventStream(w, r, func(ctx context.Context, emit func(backend.HistoryPage) error) error {
		return d.SubmitDecodedWatch(ctx, in, sub, time.Now(), emit)
	}, func(err error) { httpFailureErr(w, err) })
}
