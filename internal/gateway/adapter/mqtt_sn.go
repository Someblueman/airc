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

// ServeMQTTSN owns and closes conn after validating its arguments. This QoS -1 profile accepts
// encrypted signed Send intent, with no successful publication acknowledgement.
// One bounded receive/admission runs at a time; there is no per-source state or
// user-space pending queue. Kernel/network loss before intake remains possible.
func ServeMQTTSN(parent context.Context, conn net.PacketConn, d *dispatch.Dispatcher, requests chan struct{}, options config.MQTTSN) error {
	if conn == nil || conn.LocalAddr() == nil || conn.LocalAddr().Network() != "udp" || d == nil || cap(requests) < 1 || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return store.ErrInvalid
	}
	routes := make(map[uint16]config.MQTTSNRoute, len(options.Routes))
	for _, route := range options.Routes {
		if _, exists := routes[route.TopicID]; exists || route.TopicID == 0 || route.TopicID == 0xffff || route.Principal == "" {
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
		if admitMQTTSN(ctx, packet, routes, d, requests) == mqttSNUnknownTopic {
			// Section 6.7 recommends invalid-topic PUBACK. Only authenticated,
			// fresh encrypted Send intent can trigger this seven-byte error.
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

func admitMQTTSN(ctx context.Context, packet mqttsnwire.Publish, routes map[uint16]config.MQTTSNRoute, d *dispatch.Dispatcher, requests chan struct{}) mqttSNAdmission {
	if ctx.Err() != nil {
		return mqttSNRefused
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return mqttSNRefused
	}
	verified, err := d.AuthenticateDetached(packet.Payload, true, time.Now())
	if err != nil || verified.Operation() != core.Send {
		return mqttSNRefused
	}
	route, ok := routes[packet.TopicID]
	if !ok {
		return mqttSNUnknownTopic
	}
	target, ok := verified.Target()
	if !ok || verified.PrincipalID() != route.Principal || target != route.Target {
		return mqttSNRefused
	}
	admission, cancel := context.WithTimeout(ctx, mqttSNDeadline)
	defer cancel()
	if _, err := d.DetachedVerified(admission, verified, time.Now()); err != nil {
		return mqttSNRefused
	}
	return mqttSNAdmitted
}
