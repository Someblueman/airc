package adapter

import (
	"context"
	"encoding/binary"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/mqttsnwire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const mqttSNDeadline = 3 * time.Second

// ServeMQTTSN owns and closes conn after validating its arguments. This QoS -1
// profile accepts a send as plain contract JSON naming its author (and the
// account token, in clear, for a registered author), on a predefined topic
// mapped to the request's exact target. There is no publication
// acknowledgement.
// One bounded receive/admission runs at a time; there is no per-source state or
// user-space pending queue. Kernel/network loss before intake remains possible.
func ServeMQTTSN(parent context.Context, conn net.PacketConn, d *dispatch.Dispatcher, requests chan struct{}, options config.MQTTSN) error {
	if conn == nil || conn.LocalAddr() == nil || conn.LocalAddr().Network() != "udp" || d == nil || !d.AuthorAccess() || cap(requests) < 1 || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return store.ErrInvalid
	}
	routes := make(map[uint16]config.MQTTSNRoute, len(options.Routes))
	for _, route := range options.Routes {
		if _, exists := routes[route.TopicID]; exists || route.TopicID == 0 || route.TopicID == 0xffff || route.Target.Normalize() != nil {
			return store.ErrInvalid
		}
		routes[route.TopicID] = route
	}
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		cancel()
		_ = conn.Close()
		if !stop() {
			<-closed
		}
	}()
	buffer := make([]byte, mqttsnwire.MaxPacketBytes+1)
	defer clear(buffer)
	for ctx.Err() == nil {
		n, source, err := conn.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !validPacketDestination(source) {
			clear(buffer[:n])
			continue
		}
		packet, err := mqttsnwire.Decode(buffer[:n])
		clear(buffer[:n])
		if err != nil {
			continue
		}
		if admitAuthorMQTTSN(ctx, packet, routes, d, requests, source) == mqttSNUnknownTopic {
			// Section 6.7 recommends invalid-topic PUBACK. Only a well-formed
			// send can trigger this seven-byte error to its source.
			// It is neither an intake acknowledgement nor a delivery receipt.
			reply := []byte{7, 0x0d, 0, 0, 0, 0, 2}
			binary.BigEndian.PutUint16(reply[2:4], packet.TopicID)
			if err := conn.SetWriteDeadline(time.Now().Add(mqttSNDeadline)); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			_, _ = conn.WriteTo(reply, source)
		}
	}
	return nil
}

type mqttSNAdmission byte

const (
	mqttSNRefused mqttSNAdmission = iota
	mqttSNUnknownTopic
	mqttSNAdmitted
)

// admitAuthorMQTTSN admits a plain send on a predefined topic whose target
// is the request's own. The source IP is AIRC's rate source; it confers no
// identity.
func admitAuthorMQTTSN(ctx context.Context, packet mqttsnwire.Publish, routes map[uint16]config.MQTTSNRoute, d *dispatch.Dispatcher, requests chan struct{}, source net.Addr) mqttSNAdmission {
	if ctx.Err() != nil {
		return mqttSNRefused
	}
	sub, err := core.DecodeSubmission([]byte(packet.Payload))
	if err != nil || sub.Request.Operation != core.Send || sub.Request.Target == nil {
		return mqttSNRefused
	}
	route, ok := routes[packet.TopicID]
	if !ok {
		return mqttSNUnknownTopic
	}
	target := *sub.Request.Target
	if target.Normalize() != nil || target != route.Target {
		return mqttSNRefused
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return mqttSNRefused
	}
	admission, cancel := context.WithTimeout(ctx, mqttSNDeadline)
	defer cancel()
	if _, err := d.SubmitDecoded(admission, dispatch.Ingress{Source: d.Source(source)}, sub, time.Now()); err != nil {
		return mqttSNRefused
	}
	return mqttSNAdmitted
}
