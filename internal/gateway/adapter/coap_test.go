package adapter

import (
	"bytes"
	"context"
	"testing"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/pool"
)

func TestCoAPLedgerBoundsUniqueState(t *testing.T) {
	l := newCoAPLedger(nil)
	for i := 0; i < coapMaxMIDs; i++ {
		r := pool.NewMessage(context.Background())
		r.SetMessageID(int32(i))
		r.SetToken(message.Token{byte(i), 1})
		if drop, err := l.monitor(nil, r); drop || err != nil {
			t.Fatalf("admit %d: drop=%v err=%v", i, drop, err)
		}
	}
	r := pool.NewMessage(context.Background())
	r.SetMessageID(coapMaxMIDs + 1)
	r.SetToken(message.Token{0xff, 0xff})
	if drop, err := l.monitor(nil, r); !drop || err == nil {
		t.Fatalf("expected MID capacity rejection drop=%v err=%v", drop, err)
	}
}

func TestCoAPResponseCacheRefusesEvictionAndPreservesDuplicate(t *testing.T) {
	c := newRefusingCache(1)
	m := pool.NewMessage(context.Background())
	m.SetMessageID(7)
	m.SetType(message.NonConfirmable)
	m.SetCode(69)
	m.SetBody(bytes.NewReader([]byte("first")))
	if err := c.Store("7", m); err != nil {
		t.Fatal(err)
	}
	changed := pool.NewMessage(context.Background())
	changed.SetMessageID(7)
	changed.SetType(message.NonConfirmable)
	changed.SetCode(69)
	changed.SetBody(bytes.NewReader([]byte("changed")))
	if err := c.Store("7", changed); err != nil {
		t.Fatal(err)
	}
	n := pool.NewMessage(context.Background())
	n.SetMessageID(8)
	n.SetType(message.NonConfirmable)
	n.SetCode(69)
	n.SetBody(bytes.NewReader([]byte("second")))
	if err := c.Store("8", n); err == nil {
		t.Fatal("expected bounded cache refusal")
	}
	out := pool.NewMessage(context.Background())
	ok, err := c.Load("7", out)
	if err != nil || !ok {
		t.Fatalf("load cached response ok=%v err=%v", ok, err)
	}
	body, err := out.ReadBody()
	if err != nil || string(body) != "first" {
		t.Fatalf("cached body=%q err=%v", body, err)
	}
}

func TestCoAPLedgerUsesBlockSZXForOffsets(t *testing.T) {
	l := newCoAPLedger(nil)
	valid := pool.NewMessage(context.Background())
	valid.SetMessageID(100)
	valid.SetToken(message.Token{100})
	valid.SetOptionUint32(message.Block1, (20<<4)|5|8)
	valid.SetBody(bytes.NewReader(bytes.Repeat([]byte{'x'}, 512)))
	if drop, err := l.monitor(nil, valid); drop || err != nil {
		t.Fatalf("valid SZX512 block rejected: drop=%v err=%v", drop, err)
	}
	tooFar := pool.NewMessage(context.Background())
	tooFar.SetMessageID(101)
	tooFar.SetToken(message.Token{101})
	tooFar.SetOptionUint32(message.Block1, (32<<4)|5|8)
	tooFar.SetBody(bytes.NewReader(bytes.Repeat([]byte{'x'}, 512)))
	if drop, err := l.monitor(nil, tooFar); !drop || err == nil {
		t.Fatalf("expected aggregate offset rejection: drop=%v err=%v", drop, err)
	}
}

func TestCoAPLedgerRetainsMalformedFinalBlockReservation(t *testing.T) {
	l := newCoAPLedger(nil)
	block := func(mid int32, token byte, number uint32, more bool) *pool.Message {
		m := pool.NewMessage(context.Background())
		m.SetMessageID(mid)
		m.SetToken(message.Token{token})
		value := (number << 4) | 5
		if more {
			value |= 8
		}
		m.SetOptionUint32(message.Block1, value)
		m.SetBody(bytes.NewReader(bytes.Repeat([]byte{'x'}, 512)))
		return m
	}
	if drop, err := l.monitor(nil, block(200, 1, 0, true)); drop || err != nil {
		t.Fatalf("first block: drop=%v err=%v", drop, err)
	}
	// The offset is deliberately wrong for the retained 512-byte assembly.
	if drop, err := l.monitor(nil, block(201, 1, 2, false)); drop || err != nil {
		t.Fatalf("wrong final block should be admitted for library validation: drop=%v err=%v", drop, err)
	}
	for i := byte(2); i <= 4; i++ {
		if drop, err := l.monitor(nil, block(int32(210+i), i, 0, true)); drop || err != nil {
			t.Fatalf("assembly %d: drop=%v err=%v", i, drop, err)
		}
	}
	if drop, err := l.monitor(nil, block(220, 9, 0, true)); !drop || err == nil {
		t.Fatalf("fifth active assembly should be rejected: drop=%v err=%v", drop, err)
	}
}

func TestCoAPLedgerBlock1BoundsAndBlock2DoesNotConsumeAssemblySlots(t *testing.T) {
	l := newCoAPLedger(nil)
	tooLarge := pool.NewMessage(context.Background())
	tooLarge.SetMessageID(1)
	tooLarge.SetToken(message.Token{1})
	tooLarge.SetOptionUint32(message.Block1, (16<<4)|8)
	tooLarge.SetOptionUint32(message.Size1, coapMaxRequest)
	if drop, err := l.monitor(nil, tooLarge); !drop || err == nil {
		t.Fatalf("expected block offset rejection drop=%v err=%v", drop, err)
	}
	badSZX := pool.NewMessage(context.Background())
	badSZX.SetMessageID(2)
	badSZX.SetToken(message.Token{2})
	badSZX.SetOptionUint32(message.Block1, 7|8)
	if drop, err := l.monitor(nil, badSZX); !drop || err == nil {
		t.Fatalf("expected SZX rejection drop=%v err=%v", drop, err)
	}
	for i := 0; i < coapMaxBlocks+2; i++ {
		r := pool.NewMessage(context.Background())
		r.SetMessageID(int32(20 + i))
		r.SetToken(message.Token{byte(20 + i)})
		r.SetOptionUint32(message.Block2, uint32(i<<4)|8)
		if drop, err := l.monitor(nil, r); drop || err != nil {
			t.Fatalf("Block2 incorrectly consumed Block1 slots at %d: drop=%v err=%v", i, drop, err)
		}
	}
}

func TestCoAPResponseCacheRejectsOversizedWire(t *testing.T) {
	c := newRefusingCache(1)
	m := pool.NewMessage(context.Background())
	m.SetMessageID(1)
	m.SetType(message.NonConfirmable)
	m.SetCode(69)
	m.SetBody(bytes.NewReader(bytes.Repeat([]byte{'x'}, 2000)))
	if err := c.Store("oversize", m); err == nil {
		t.Fatal("expected wire-size rejection")
	}
}

func TestCoAPLedgerReleasesExactFinalBlock(t *testing.T) {
	l := newCoAPLedger(nil)
	block := func(mid int32, token byte, number uint32, more bool) *pool.Message {
		m := pool.NewMessage(context.Background())
		m.SetMessageID(mid)
		m.SetToken(message.Token{token})
		value := (number << 4) | 5
		if more {
			value |= 8
		}
		m.SetOptionUint32(message.Block1, value)
		m.SetBody(bytes.NewReader(bytes.Repeat([]byte{'x'}, 512)))
		return m
	}
	if drop, err := l.monitor(nil, block(300, 1, 0, true)); drop || err != nil {
		t.Fatal(err)
	}
	if drop, err := l.monitor(nil, block(301, 1, 1, false)); drop || err != nil {
		t.Fatal(err)
	}
	for i := byte(2); i <= 5; i++ {
		if drop, err := l.monitor(nil, block(int32(310)+int32(i), i, 0, true)); drop || err != nil {
			t.Fatalf("released slot not reusable at %d: %v", i, err)
		}
	}
}

func TestCoAPLedgerSingleBlockAndNonzeroInitialOffsets(t *testing.T) {
	l := newCoAPLedger(nil)
	one := pool.NewMessage(context.Background())
	one.SetMessageID(400)
	one.SetToken(message.Token{40})
	one.SetOptionUint32(message.Block1, 5)
	one.SetBody(bytes.NewReader(bytes.Repeat([]byte{'x'}, 16)))
	if drop, err := l.monitor(nil, one); drop || err != nil {
		t.Fatalf("single-block M0: drop=%v err=%v", drop, err)
	}
	first := pool.NewMessage(context.Background())
	first.SetMessageID(401)
	first.SetToken(message.Token{41})
	first.SetOptionUint32(message.Block1, (1<<4)|5|8)
	first.SetBody(bytes.NewReader(bytes.Repeat([]byte{'x'}, 512)))
	if drop, err := l.monitor(nil, first); drop || err != nil {
		t.Fatalf("nonzero initial block: drop=%v err=%v", drop, err)
	}
	wrong := pool.NewMessage(context.Background())
	wrong.SetMessageID(402)
	wrong.SetToken(message.Token{41})
	wrong.SetOptionUint32(message.Block1, (2<<4)|5)
	wrong.SetBody(bytes.NewReader(bytes.Repeat([]byte{'x'}, 512)))
	if drop, err := l.monitor(nil, wrong); drop || err != nil {
		t.Fatalf("wrong final should retain: drop=%v err=%v", drop, err)
	}
	correct := pool.NewMessage(context.Background())
	correct.SetMessageID(403)
	correct.SetToken(message.Token{41})
	correct.SetOptionUint32(message.Block1, 5)
	correct.SetBody(bytes.NewReader(bytes.Repeat([]byte{'x'}, 512)))
	if drop, err := l.monitor(nil, correct); drop || err != nil {
		t.Fatalf("offset-zero final: drop=%v err=%v", drop, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.blocks) != 0 {
		t.Fatalf("expected exact final to release reservation, got %d", len(l.blocks))
	}
}
