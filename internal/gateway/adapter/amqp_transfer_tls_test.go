//go:build darwin || linux

package adapter

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
)

func TestAMQPResponseMultipartOverOwnedTLSConnection(t *testing.T) {
	c, client, cancel := amqpOwnedFixture(t)
	// Independently spelled data-section encoding, with a full-width descriptor
	// and binary32 length. This is a transport test, without a signed dispatcher.
	body := bytes.Repeat([]byte("response"), 4096)
	encoded := append([]byte{0, 0x80, 0, 0, 0, 0, 0, 0, 0, 0x75, 0xb0, 0, 0, 0, 0}, body...)
	binary.BigEndian.PutUint32(encoded[11:15], uint32(len(body)))
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		run := func() error {
			links := amqpLinkFixture()
			links.negotiation = c.negotiation
			s := newAMQPSession(c.negotiation)
			attach, err := c.readBefore(time.Time{})
			if err != nil {
				return err
			}
			reply, condition := links.attach(attach)
			if condition != "" {
				return errors.New(condition)
			}
			if err := c.write(reply); err != nil {
				return err
			}
			l := links.links[0]
			if condition := s.queueResponse(l, encoded, []byte("wire"), time.Now()); condition != "" {
				return errors.New(condition)
			}
			for !l.outbound.complete {
				p, err := c.readBefore(links.deliveryDeadline())
				if err != nil {
					return err
				}
				if p.Kind == 0 {
					continue
				}
				reply, condition := s.flow(p, &links)
				if condition != "" {
					return errors.New(condition)
				}
				if reply.Kind != 0 {
					if err := c.write(reply); err != nil {
						return err
					}
				}
				// Deliberately fragment below the negotiated 64K ceiling to exercise
				// continuation frames and window replenishment over the actual socket.
				if err := s.flushResponse(l, 512, time.Now(), c.writeBefore); err != nil {
					return err
				}
			}
			return nil
		}
		done <- run()
	}()
	defer func() { cancel(); c.close(); <-finished }()
	amqpClientWrite(t, client, amqpAttach(false, 0, "response"))
	if p := amqpClientRead(t, client); p.Kind != 0x12 {
		t.Fatal(p)
	}
	p := amqpwire.Performative{Kind: 0x13, Fields: []amqpwire.Value{{Code: 0x70, Number: 1}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 7}, {Code: 0x70, Number: 8}, {Code: 0x70}, {Code: 0x70}, {Code: 0x70, Number: 1}}}
	amqpClientWrite(t, client, p)
	var received []byte
	frames := 0
	for {
		p := amqpClientRead(t, client)
		if p.Kind != 0x14 || p.Fields[0].Number != 0 {
			t.Fatal(p)
		}
		if frames == 0 && (p.Fields[1].Number != 0 || string(p.Fields[2].Data) != "wire") {
			t.Fatal(p)
		}
		frames++
		received = append(received, p.Payload...)
		if p.Fields[5].Number == 0 {
			break
		}
		if frames%amqpWindow == 0 {
			flow := amqpwire.Performative{Kind: 0x13, Fields: []amqpwire.Value{{Code: 0x70, Number: uint64(frames + 1)}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 7}, {Code: 0x70, Number: 8}}}
			amqpClientWrite(t, client, flow)
		}
	}
	if frames <= amqpWindow || !bytes.Equal(received, encoded) {
		t.Fatal("multipart bytes differ", frames, len(received))
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("transfer owner did not finish")
	}
}

func TestAMQPConnectionUsesAbsoluteDeliveryDeadlines(t *testing.T) {
	c, _, _ := amqpOwnedFixture(t)
	_, err := c.readBefore(time.Now().Add(-time.Second))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatal("read extended expired delivery", err)
	}
	err = c.writeBefore(amqpwire.Performative{}, time.Now().Add(-time.Second))
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatal("write extended expired delivery", err)
	}
}
