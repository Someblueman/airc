package adapter

import (
	"context"
	"testing"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
)

func TestTCPResponseSZXFitsPeerFrameWithWorstOptions(t *testing.T) {
	m := pool.NewMessage(context.Background())
	m.SetToken(message.Token("12345678"))
	m.SetCode(codes.Content)
	m.SetOptionString(message.URIPath, "v1/results")
	szx, ok := tcpResponseSZX(m, 700)
	if !ok {
		t.Fatal("no block size fits 700-byte peer frame")
	}
	if szx >= blockwise.SZX1024 {
		t.Fatalf("selected oversized SZX %v", szx)
	}
	if _, ok := tcpResponseSZX(m, 20); ok {
		t.Fatal("accepted a frame budget too small for protocol overhead")
	}
}

func TestTCPDownshiftBlock2PreservesByteOffset(t *testing.T) {
	m := pool.NewMessage(context.Background())
	m.SetToken(message.Token("12345678"))
	m.SetCode(codes.GET)
	block, err := blockwise.EncodeBlockOption(blockwise.SZX1024, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	m.SetOptionUint32(message.Block2, block)
	if err := tcpDownshiftBlock2(m, 700); err != nil {
		t.Fatal(err)
	}
	got, num, more, err := func() (blockwise.SZX, int64, bool, error) {
		v, err := m.GetOptionUint32(message.Block2)
		if err != nil {
			return 0, 0, false, err
		}
		return blockwise.DecodeBlockOption(v)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if got >= blockwise.SZX1024 || !more || num*got.Size() != 2*blockwise.SZX1024.Size() {
		t.Fatalf("downshift got szx=%v num=%d more=%v", got, num, more)
	}
}

func TestTCPDownshiftLeavesSmallRequestUntouched(t *testing.T) {
	m := pool.NewMessage(context.Background())
	m.SetCode(codes.GET)
	block, err := blockwise.EncodeBlockOption(blockwise.SZX256, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	m.SetOptionUint32(message.Block2, block)
	if err := tcpDownshiftBlock2(m, 700); err != nil {
		t.Fatal(err)
	}
	got, _, _, err := func() (blockwise.SZX, int64, bool, error) {
		v, err := m.GetOptionUint32(message.Block2)
		if err != nil {
			return 0, 0, false, err
		}
		return blockwise.DecodeBlockOption(v)
	}()
	if err != nil || got != blockwise.SZX256 {
		t.Fatalf("small block changed szx=%v err=%v", got, err)
	}
}
