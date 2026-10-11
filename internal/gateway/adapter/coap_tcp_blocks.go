package adapter

import (
	"context"
	"math"

	"github.com/Someblueman/airc/internal/gateway/store"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/tcp/coder"
)

// tcpResponseSZX chooses a block size from an actually encoded continuation
// frame. The token and response options are retained, while worst-case Block2
// and Size2 widths are included in the candidate.
func tcpResponseSZX(req *pool.Message, peerMax uint32) (blockwise.SZX, bool) {
	for value := int(blockwise.SZX1024); value >= int(blockwise.SZX16); value-- {
		szx := blockwise.SZX(value)
		options, err := req.Options().Clone()
		if err != nil {
			continue
		}
		candidate := pool.NewMessage(context.Background())
		candidate.SetMessage(message.Message{
			Token:   append(message.Token(nil), req.Token()...),
			Code:    req.Code(),
			Type:    req.Type(),
			Options: options,
			Payload: make([]byte, int(szx.Size())),
		})
		candidate.SetOptionUint32(message.Block2, uint32((1<<20-1)<<4)|8|uint32(szx))
		candidate.SetOptionUint32(message.Size2, math.MaxUint32)
		wire, err := candidate.MarshalWithEncoder(coder.DefaultCoder)
		if err == nil && uint64(len(wire)) <= uint64(peerMax) {
			return szx, true
		}
	}
	return blockwise.SZX16, false
}

// tcpDownshiftBlock2 preserves the byte offset when a peer requests a block
// larger than its advertised frame budget. The request remains immutable when
// it is already within the budget.
func tcpDownshiftBlock2(req *pool.Message, peerMax uint32) error {
	if !req.HasOption(message.Block2) {
		return nil
	}
	block, err := req.GetOptionUint32(message.Block2)
	if err != nil {
		return store.ErrInvalid
	}
	szx, number, more, err := blockwise.DecodeBlockOption(block)
	if err != nil {
		return err
	}
	if szx == blockwise.SZXBERT {
		return store.ErrInvalid
	}
	// Request URI options are absent from responses; budget the largest
	// supported response metadata, including Observe, instead.
	template := pool.NewMessage(context.Background())
	template.SetToken(req.Token())
	template.SetCode(codes.Content)
	template.SetContentFormat(message.AppOctets)
	template.SetOptionUint32(message.MaxAge, 0)
	template.SetObserve(0xffffff)
	selected, ok := tcpResponseSZX(template, peerMax)
	if !ok {
		return store.ErrCapacity
	}
	if selected >= szx {
		return nil
	}
	oldSize := szx.Size()
	newSize := selected.Size()
	if oldSize <= 0 || newSize <= 0 || number > (1<<20)-1 {
		return nil
	}
	offset := number * oldSize
	if offset%newSize != 0 {
		return nil
	}
	newNumber := offset / newSize
	newBlock, err := blockwise.EncodeBlockOption(selected, newNumber, more)
	if err != nil {
		return err
	}
	req.SetOptionUint32(message.Block2, newBlock)
	return nil
}
