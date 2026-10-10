package adapter

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/tcp/coder"
)

// Standard Observe clients fetch subsequent blocks using a fresh token. The
// observation query identifies this association-owned snapshot independently
// of those request tokens. AGD1 bytes and indexes remain unchanged.
type tcpBlockTransfer struct {
	done       chan struct{}
	nextOffset int64
	duplicates uint8
	expires    time.Time
	body       []byte
	options    message.Options
}

func (c *coapTCPConn) cancelBlockTransfer(token message.Token) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if transfer := c.transfers[string(token)]; transfer != nil {
		delete(c.transfers, string(token))
		close(transfer.done)
	}
}

func (c *coapTCPConn) waitBlockTransfer(ctx context.Context, token message.Token) error {
	c.mu.Lock()
	transfer := c.transfers[string(token)]
	c.mu.Unlock()
	if transfer == nil {
		return nil
	}
	ctx, cancel := context.WithDeadline(ctx, transfer.expires)
	defer cancel()
	select {
	case <-transfer.done:
		return nil
	case <-ctx.Done():
		// The final block handler can complete before Observe cancellation is
		// handled while this notification writer has not yet been scheduled.
		select {
		case <-transfer.done:
			return nil
		default:
		}
		_ = c.Close()
		return ctx.Err()
	}
}

func (p *coapTCPPeer) writeObserveBlocks(ctx context.Context, m *pool.Message, observe uint32, szx blockwise.SZX) error {
	body, err := m.ReadBody()
	if err != nil || len(body) > agd1DatagramBytes {
		return store.ErrCapacity
	}
	options, err := m.Options().Clone()
	if err != nil {
		return err
	}
	transfer := &tcpBlockTransfer{done: make(chan struct{}), body: append([]byte(nil), body...), options: options.Remove(message.Observe), expires: time.Now().Add(3 * time.Second)}
	p.carrier.mu.Lock()
	if p.carrier.transfers == nil {
		p.carrier.transfers = make(map[string]*tcpBlockTransfer)
	}
	key := string(m.Token())
	if len(p.carrier.transfers) >= 4 || p.carrier.transfers[key] != nil {
		p.carrier.mu.Unlock()
		return store.ErrCapacity
	}
	p.carrier.transfers[key] = transfer
	p.carrier.mu.Unlock()
	first := p.cc.AcquireMessage(m.Context())
	defer p.cc.ReleaseMessage(first)
	first.ResetOptionsTo(transfer.options)
	first.SetObserve(observe)
	first.SetCode(codes.Content)
	first.SetToken(m.Token())
	block, err := blockwise.EncodeBlockOption(szx, 0, true)
	if err != nil {
		return err
	}
	first.SetOptionUint32(message.Block2, block)
	first.SetOptionUint32(message.Size2, uint32(len(body)))
	first.SetBody(bytes.NewReader(body[:int(szx.Size())]))
	p.carrier.mu.Lock()
	transfer.nextOffset = szx.Size()
	p.carrier.mu.Unlock()
	if err := p.carrier.writeMessage(ctx, first); err != nil {
		return err
	}
	if observe == 0 {
		// The reader must return so it can service readiness continuations.
		// The watch writer waits for this transfer before its first page.
		return nil
	}
	return p.carrier.waitBlockTransfer(ctx, m.Token())
}

func (p *coapTCPPeer) serveObserveBlock(w coapResponder, req *pool.Message) bool {
	path, err := req.Path()
	if err != nil || path != "/v1/coap/observe" || req.Code() != codes.GET || !req.HasOption(message.Block2) {
		return false
	}
	bad := func() bool { _ = w.SetResponse(codes.BadRequest, message.TextPlain, nil); return true }
	if req.HasOption(message.Observe) {
		return bad()
	}
	size, err := req.BodySize()
	if err != nil || size != 0 || len(req.Token()) != 8 {
		return bad()
	}
	token, ok := tcpObservationQuery(req)
	if !ok {
		return bad()
	}
	p.carrier.mu.Lock()
	transfer := p.carrier.transfers[string(token)]
	nextOffset := int64(0)
	if transfer != nil {
		nextOffset = transfer.nextOffset
	}
	p.carrier.mu.Unlock()
	if transfer == nil || !time.Now().Before(transfer.expires) {
		return bad()
	}
	block, err := req.GetOptionUint32(message.Block2)
	if err != nil {
		return bad()
	}
	szx, number, _, err := blockwise.DecodeBlockOption(block)
	if err != nil || szx > blockwise.SZX1024 {
		return bad()
	}
	offset := number * szx.Size()
	if offset < 0 || offset >= int64(len(transfer.body)) || offset > nextOffset {
		return bad()
	}
	if offset < nextOffset {
		p.carrier.mu.Lock()
		if transfer.duplicates >= 8 {
			p.carrier.mu.Unlock()
			return bad()
		}
		transfer.duplicates++
		p.carrier.mu.Unlock()
	}
	end := min(offset+szx.Size(), int64(len(transfer.body)))
	if err := w.SetResponse(codes.Content, message.AppOctets, bytes.NewReader(transfer.body[offset:end])); err != nil {
		return bad()
	}
	w.Message().ResetOptionsTo(transfer.options)
	w.Message().SetOptionUint32(message.Block2, block)
	if end == int64(len(transfer.body)) {
		w.Message().SetOptionUint32(message.Block2, block&^8)
	} else {
		w.Message().SetOptionUint32(message.Block2, block|8)
	}
	w.Message().SetOptionUint32(message.Size2, uint32(len(transfer.body)))
	// The only continuation authority is this association's random observation
	// token; it cannot admit a new operation or cross to another association.
	p.carrier.state.mu.Lock()
	p.carrier.state.authenticated[string(req.Token())] = true
	p.carrier.state.mu.Unlock()
	writeCtx, finish := context.WithDeadline(w.Message().Context(), transfer.expires)
	defer finish()
	w.Message().SetContext(writeCtx)
	if err := p.WriteMessage(w.Message()); err == nil {
		p.carrier.mu.Lock()
		if current := p.carrier.transfers[string(token)]; current == transfer && offset == transfer.nextOffset {
			transfer.nextOffset = end
			if end == int64(len(transfer.body)) {
				delete(p.carrier.transfers, string(token))
				close(transfer.done)
			}
		}
		p.carrier.mu.Unlock()
	}
	// It has already passed through the bounded writer; suppress a second send.
	w.Message().Reset()
	return true
}

func tcpObservationQuery(req *pool.Message) (message.Token, bool) {
	query := ""
	count := 0
	for _, option := range req.Options() {
		if option.ID == message.URIQuery {
			count++
			if count != 1 {
				return nil, false
			}
			query = string(option.Value)
		}
	}
	if !strings.HasPrefix(query, "observation=") {
		return nil, false
	}
	token, err := hex.DecodeString(strings.TrimPrefix(query, "observation="))
	return token, err == nil && len(token) == 8
}

func (p *coapTCPPeer) validObserveRoute(req *pool.Message) bool {
	path, err := req.Path()
	if err != nil || path != "/v1/coap/observe" || req.Code() != codes.GET || !req.HasOption(message.Observe) {
		return true
	}
	token, ok := tcpObservationQuery(req)
	if !ok {
		observe, err := req.Observe()
		return !req.HasOption(message.URIQuery) && err == nil && (observe == 1 || tcpObserveFits(p.carrier.peerLimit()))
	}
	return bytes.Equal(token, req.Token())
}

func (c *coapTCPConn) hasExpiredTransfer(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, transfer := range c.transfers {
		if !now.Before(transfer.expires) {
			return true
		}
	}
	return false
}

func tcpObserveFits(maximum uint32) bool {
	m := pool.NewMessage(context.Background())
	m.SetCode(codes.Content)
	m.SetToken(make(message.Token, 8))
	m.SetContentFormat(message.AppOctets)
	m.SetOptionUint32(message.MaxAge, 0)
	m.SetObserve(0xffffff)
	m.SetBody(bytes.NewReader(make([]byte, agd1DatagramBytes)))
	wire, err := m.MarshalWithEncoder(coder.DefaultCoder)
	return err == nil && uint64(len(wire)) <= uint64(maximum)
}
