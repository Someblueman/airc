package fallback

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

// The contract JSON every gateway carrier takes (docs/gateway/contract-v1.md).
type author struct {
	Nick  string `json:"nick"`
	Token string `json:"token,omitempty"`
}

type target struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type request struct {
	Version   int     `json:"version"`
	RequestID string  `json:"request_id"`
	Operation string  `json:"operation"`
	Target    *target `json:"target,omitempty"`
	Message   string  `json:"message,omitempty"`
	ParentID  string  `json:"parent_id,omitempty"`
	LookupID  string  `json:"lookup_id,omitempty"`
	Start     string  `json:"start,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Author    *author `json:"author,omitempty"`
	Password  string  `json:"password,omitempty"`
}

type failure struct {
	Code         string `json:"code"`
	Transient    bool   `json:"transient"`
	UpstreamCode int    `json:"upstream_code,omitempty"`
}

type result struct {
	RequestID           string   `json:"request_id"`
	State               string   `json:"state"`
	MessageID           string   `json:"message_id"`
	Persisted           *bool    `json:"persisted"`
	RecipientConnected  *bool    `json:"recipient_connected"`
	DuplicateRisk       bool     `json:"duplicate_risk"`
	AcceptanceUncertain bool     `json:"acceptance_uncertain"`
	Failure             *failure `json:"error"`
}

type page struct {
	Messages []*irc.MessageMetadata `json:"messages"`
	Cursor   string                 `json:"cursor"`
	Status   string                 `json:"status"`
}

// detached is the DetachedResult every carrier answers with.
type detached struct {
	Operation    string  `json:"operation"`
	Result       *result `json:"result"`
	Page         *page   `json:"page"`
	Capabilities *struct {
		Carriers []string `json:"carriers"`
	} `json:"capabilities"`
}

// gatewayError is the gateway's answer when it refuses or cannot run a request.
type gatewayError struct{ failure failure }

func (e *gatewayError) Error() string {
	if e.failure.UpstreamCode != 0 {
		return fmt.Sprintf("gateway error %s (AIRC %d)", e.failure.Code, e.failure.UpstreamCode)
	}
	return "gateway error " + e.failure.Code
}

// definiteCode reports gateway codes that are the server's final answer. The
// others (upstream_unavailable, capacity_exceeded, delivery_uncertain,
// unsupported_operation) describe this carrier or this moment.
func definiteCode(code string) bool {
	switch code {
	case "nick_registered", "nick_in_use", "authentication_failed", "forbidden", "invalid_request", "id_conflict":
		return true
	}
	return false
}

func refusalOf(e Endpoint, f failure) error {
	err := &gatewayError{failure: f}
	if definiteCode(f.Code) {
		return &Refusal{Endpoint: e, Code: f.Code, UpstreamCode: f.UpstreamCode, Err: err}
	}
	return err
}

// connectFailed reports a request that never left: its connection could not
// be opened (HTTP connects inside its first call).
func connectFailed(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// gatewayConn carries contract requests to one gateway endpoint.
type gatewayConn interface {
	call(ctx context.Context, r request) (detached, error)
	Close() error
}

func (c *Client) author() *author { return &author{Nick: c.Nick, Token: c.Token} }

func (c *Client) dialGateway(ctx context.Context, e Endpoint) (gatewayConn, error) {
	switch e.Name {
	case "http":
		return c.dialHTTP(e), nil
	case "websocket":
		return c.dialWebSocket(ctx, e)
	case "framed", "unix":
		return c.dialFramed(ctx, e)
	}
	return nil, fmt.Errorf("carrier %s is not supported by this client", e.Name)
}

// callGateway runs one request on a fresh connection to e.
func (c *Client) callGateway(ctx context.Context, e Endpoint, r request) (detached, error) {
	conn, err := c.dialGateway(ctx, e)
	if err != nil {
		return detached{}, err
	}
	defer conn.Close()
	return c.call(ctx, conn, e, r)
}

func (c *Client) call(ctx context.Context, conn gatewayConn, e Endpoint, r request) (detached, error) {
	r.Version, r.Author, r.Password = 1, c.author(), c.Password
	ctx, cancel := context.WithTimeout(ctx, c.callTimeout())
	defer cancel()
	d, err := conn.call(ctx, r)
	var refused *gatewayError
	if errors.As(err, &refused) {
		return d, refusalOf(e, refused.failure)
	}
	return d, err
}

func targetOf(name string) *target {
	if protocol.IsChannel(name) {
		return &target{Kind: "room", Name: name}
	}
	return &target{Kind: "direct", Name: name}
}

// gatewayPost submits m and follows its job until AIRC accepts or refuses it.
// The journal keys the job by author and request ID, so resubmitting on
// another carrier finds this job instead of posting again.
func (c *Client) gatewayPost(ctx context.Context, e Endpoint, m Message) (*Receipt, error) {
	conn, err := c.dialGateway(ctx, e)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	r := request{RequestID: m.RequestID, Operation: "send", Target: targetOf(m.Target), Message: m.Body}
	if m.ParentID != "" {
		r.Operation, r.ParentID = "reply", m.ParentID
	}
	d, err := c.call(ctx, conn, e, r)
	if err != nil {
		if Definite(err) || connectFailed(err) {
			return nil, err
		}
		return nil, &uncertain{err}
	}
	return c.follow(ctx, conn, e, d)
}

func (c *Client) gatewayLookup(ctx context.Context, e Endpoint, requestID string) (*Receipt, error) {
	conn, err := c.dialGateway(ctx, e)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	d, err := c.call(ctx, conn, e, request{RequestID: "lookup", Operation: "receipt.get", LookupID: requestID})
	if err != nil {
		return nil, err
	}
	receipt, err := c.follow(ctx, conn, e, d)
	var u *uncertain
	if errors.As(err, &u) {
		return nil, u.err // a lookup never posts
	}
	return receipt, err
}

// follow polls a job's receipt on the same connection until it is final.
func (c *Client) follow(ctx context.Context, conn gatewayConn, e Endpoint, d detached) (*Receipt, error) {
	delay := 50 * time.Millisecond
	for {
		r := d.Result
		if r == nil {
			return nil, &uncertain{errors.New("gateway answered without a result")}
		}
		switch r.State {
		case "accepted":
			return &Receipt{Endpoint: e, RequestID: r.RequestID, MessageID: r.MessageID, Persisted: r.Persisted, RecipientConnected: r.RecipientConnected}, nil
		case "rejected", "expired", "blocked":
			f := failure{Code: r.State}
			if r.Failure != nil {
				f = *r.Failure
			}
			if r.State != "rejected" && (r.AcceptanceUncertain || r.DuplicateRisk) {
				return nil, &uncertain{fmt.Errorf("gateway job %s with its outcome unknown (%s)", r.State, f.Code)}
			}
			return nil, &Refusal{Endpoint: e, Code: f.Code, UpstreamCode: f.UpstreamCode, Err: &gatewayError{failure: f}}
		case "uncertain":
			return nil, &uncertain{errors.New("the gateway could not confirm delivery")}
		}
		// queued, dispatching, retry_wait or credential_wait: the gateway
		// owns the job durably; wait for its outcome.
		select {
		case <-ctx.Done():
			return nil, &uncertain{fmt.Errorf("gateway job still %s: %w", r.State, ctx.Err())}
		case <-time.After(delay):
		}
		delay = min(delay*2, 500*time.Millisecond)
		var err error
		d, err = c.call(ctx, conn, e, request{RequestID: "lookup", Operation: "receipt.get", LookupID: r.RequestID})
		if err != nil {
			return nil, &uncertain{err}
		}
	}
}

func (c *Client) gatewayHistory(ctx context.Context, e Endpoint, room string, limit int) (*Page, error) {
	d, err := c.callGateway(ctx, e, request{RequestID: "history", Operation: "history.read", Target: targetOf(room), Limit: limit, Start: "recent"})
	if err != nil {
		return nil, err
	}
	if d.Page == nil {
		return nil, errors.New("gateway answered without a page")
	}
	if d.Page.Messages == nil {
		d.Page.Messages = []*irc.MessageMetadata{}
	}
	return &Page{Endpoint: e, Messages: d.Page.Messages, Status: d.Page.Status, Cursor: d.Page.Cursor}, nil
}

func (c *Client) gatewayCarriers(ctx context.Context, e Endpoint) ([]protocol.Carrier, error) {
	d, err := c.callGateway(ctx, e, request{RequestID: "capabilities", Operation: "capabilities.get"})
	if err != nil {
		return nil, err
	}
	if d.Capabilities == nil {
		return nil, errors.New("gateway answered without capabilities")
	}
	var out []protocol.Carrier
	for _, s := range d.Capabilities.Carriers {
		carrier, err := protocol.ParseCarrier(s)
		if err != nil {
			return nil, err
		}
		out = append(out, carrier)
	}
	return out, nil
}

// maxResponse bounds any one gateway answer (a history page is at most 1 MiB).
const maxResponse = 2 << 20

func (c *Client) dialer() *net.Dialer { return &net.Dialer{Timeout: c.connectTimeout()} }

// httpConn posts each request to /v1/request on one keep-alive client.
type httpConn struct {
	client *http.Client
	url    string
}

func (c *Client) dialHTTP(e Endpoint) *httpConn {
	network, address := c.address(e)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return c.dialer().DialContext(ctx, network, address)
		},
		TLSClientConfig:     c.tlsConfig(e),
		TLSHandshakeTimeout: c.connectTimeout(),
	}
	scheme, host := "http", address
	if e.TLS {
		scheme = "https"
	}
	if network == "unix" {
		host = "localhost"
	}
	return &httpConn{client: &http.Client{Transport: transport}, url: scheme + "://" + host + "/v1/request"}
}

func (h *httpConn) call(ctx context.Context, r request) (detached, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return detached{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return detached{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return detached{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return detached{}, err
	}
	if resp.StatusCode/100 != 2 {
		var refused struct {
			Error *failure `json:"error"`
		}
		if json.Unmarshal(data, &refused) == nil && refused.Error != nil && refused.Error.Code != "" {
			return detached{}, &gatewayError{failure: *refused.Error}
		}
		return detached{}, fmt.Errorf("gateway answered HTTP %d", resp.StatusCode)
	}
	var d detached
	if err := json.Unmarshal(data, &d); err != nil {
		return detached{}, fmt.Errorf("decode gateway answer: %w", err)
	}
	return d, nil
}

func (h *httpConn) Close() error {
	h.client.CloseIdleConnections()
	return nil
}

// frame is the correlated wrapper of WebSocket and framed-stream messages.
type frame struct {
	Version       int      `json:"version"`
	CorrelationID string   `json:"correlation_id"`
	Profile       string   `json:"profile,omitempty"`
	Request       *request `json:"request,omitempty"`
}

type frameAnswer struct {
	CorrelationID string    `json:"correlation_id"`
	Event         string    `json:"event"`
	Result        *detached `json:"result"`
	Error         *failure  `json:"error"`
}

var correlation atomic.Uint64

func nextCorrelation() string { return "c" + strconv.FormatUint(correlation.Add(1), 10) }

func answerOf(data []byte, id string) (detached, bool, error) {
	var a frameAnswer
	if err := json.Unmarshal(data, &a); err != nil {
		return detached{}, false, fmt.Errorf("decode gateway answer: %w", err)
	}
	if a.CorrelationID != id {
		return detached{}, false, nil
	}
	if a.Error != nil {
		return detached{}, true, &gatewayError{failure: *a.Error}
	}
	if a.Result == nil {
		return detached{}, true, fmt.Errorf("gateway answered %q without a result", a.Event)
	}
	return *a.Result, true, nil
}

type webSocketConn struct{ conn *websocket.Conn }

func (c *Client) dialWebSocket(ctx context.Context, e Endpoint) (*webSocketConn, error) {
	network, address := c.address(e)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return c.dialer().DialContext(ctx, network, address)
		},
		TLSClientConfig:     c.tlsConfig(e),
		TLSHandshakeTimeout: c.connectTimeout(),
	}
	scheme, host := "ws", address
	if e.TLS {
		scheme = "wss"
	}
	if network == "unix" {
		host = "localhost"
	}
	dialCtx, cancel := context.WithTimeout(ctx, c.connectTimeout())
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, scheme+"://"+host+"/v1/socket", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, Subprotocols: []string{"airc-gateway.v1"}})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxResponse)
	return &webSocketConn{conn: conn}, nil
}

func (w *webSocketConn) call(ctx context.Context, r request) (detached, error) {
	id := nextCorrelation()
	data, err := json.Marshal(frame{Version: 1, CorrelationID: id, Profile: "plain", Request: &r})
	if err != nil {
		return detached{}, err
	}
	if err := w.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return detached{}, err
	}
	for {
		_, reply, err := w.conn.Read(ctx)
		if err != nil {
			return detached{}, err
		}
		if d, ok, err := answerOf(reply, id); ok || err != nil {
			return d, err
		}
	}
}

func (w *webSocketConn) Close() error { return w.conn.Close(websocket.StatusNormalClosure, "") }

// framedConn speaks length-prefixed JSON frames over TCP, TLS or a Unix socket.
type framedConn struct{ conn net.Conn }

func (c *Client) dialFramed(ctx context.Context, e Endpoint) (*framedConn, error) {
	network, address := c.address(e)
	dialCtx, cancel := context.WithTimeout(ctx, c.connectTimeout())
	defer cancel()
	var conn net.Conn
	var err error
	if cfg := c.tlsConfig(e, "airc-gateway/1"); cfg != nil {
		conn, err = (&tls.Dialer{NetDialer: c.dialer(), Config: cfg}).DialContext(dialCtx, network, address)
	} else {
		conn, err = c.dialer().DialContext(dialCtx, network, address)
	}
	if err != nil {
		return nil, err
	}
	return &framedConn{conn: conn}, nil
}

func (f *framedConn) call(ctx context.Context, r request) (detached, error) {
	id := nextCorrelation()
	data, err := json.Marshal(frame{Version: 1, CorrelationID: id, Profile: "plain", Request: &r})
	if err != nil {
		return detached{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = f.conn.SetDeadline(time.Now()) })
	defer stop()
	header := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	if _, err := f.conn.Write(append(header, data...)); err != nil {
		return detached{}, err
	}
	for {
		var size [4]byte
		if _, err := io.ReadFull(f.conn, size[:]); err != nil {
			return detached{}, err
		}
		n := binary.BigEndian.Uint32(size[:])
		if n == 0 || n > maxResponse {
			return detached{}, fmt.Errorf("gateway frame of %d bytes", n)
		}
		reply := make([]byte, n)
		if _, err := io.ReadFull(f.conn, reply); err != nil {
			return detached{}, err
		}
		if d, ok, err := answerOf(reply, id); ok || err != nil {
			return d, err
		}
	}
}

func (f *framedConn) Close() error { return f.conn.Close() }
