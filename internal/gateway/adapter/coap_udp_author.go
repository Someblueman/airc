package adapter

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapNet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/net/responsewriter"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/options/config"
	coapclient "github.com/plgd-dev/go-coap/v3/udp/client"
	coapudp "github.com/plgd-dev/go-coap/v3/udp/server"
)

// coapUDPIdle closes a CoAP UDP peer's state after this long without traffic.
const coapUDPIdle = 30 * time.Second

// ServeCoAPUDP serves plain author requests as ordinary CoAP over UDP
// (RFC 7252), confirmable or not: POST /v1/coap/rpc with a plain frame, the
// Observe registration on /v1/coap/observe/register and /v1/coap/observe,
// answered in the response (piggybacked or separate) with Block2 for large
// results. Each request names its author and token: an unverified UDP source
// has no session, so there is no identify. A reply is at most one block per
// request packet, which bounds reflection to a spoofed source.
func ServeCoAPUDP(parent context.Context, conn *net.UDPConn, d *dispatch.Dispatcher, slots, requests chan struct{}) error {
	if conn == nil || !d.AuthorAccess() || cap(slots) < 1 || cap(requests) < 1 || cap(requests) > 256 {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var mu sync.Mutex
	peers := make(map[*coapclient.Conn]*coapUDPPeer)
	var work sync.WaitGroup
	lookup := func(cc *coapclient.Conn) *coapUDPPeer {
		mu.Lock()
		defer mu.Unlock()
		return peers[cc]
	}
	server := coapudp.New(
		options.WithContext(ctx),
		options.WithMaxMessageSize(coapMaxMessage),
		options.WithMTU(coapMTU),
		options.WithBlockwise(true, blockwise.SZX1024, 3*time.Second),
		options.WithReceivedMessageQueueSize(16),
		options.WithTransmission(1, time.Second, 2),
		options.WithInactivityMonitor(coapUDPIdle, func(cc *coapclient.Conn) { _ = cc.Close() }),
		options.WithErrors(func(error) {}),
		options.WithOnNewConn(func(cc *coapclient.Conn) {
			select {
			case slots <- struct{}{}:
			default:
				_ = cc.Close()
				return
			}
			state := newCoAPState(requests, "coap_udp_observe_fragments")
			// No session: a UDP source is unverified, so every request
			// names its own author.
			state.plain = &dispatch.Ingress{Source: d.Source(cc.RemoteAddr())}
			peer := &coapUDPPeer{state: state, ledger: newCoAPLedger(state.reset)}
			mu.Lock()
			peers[cc] = peer
			work.Add(1)
			mu.Unlock()
			cc.AddOnClose(func() {
				state.close()
				go func() {
					state.workers.Wait()
					mu.Lock()
					delete(peers, cc)
					mu.Unlock()
					<-slots
					work.Done()
				}()
			})
		}),
		options.WithRequestMonitor(func(cc *coapclient.Conn, req *pool.Message) (bool, error) {
			peer := lookup(cc)
			if peer == nil {
				return true, store.ErrCapacity
			}
			return peer.ledger.check(req)
		}),
		// go-coap's UDP server owns its sessions, so there is no session
		// WriteMessage to wrap as the DTLS association does. The response to
		// a received request is written by ProcessReceivedMessageWithHandler
		// after its handler returns; a failed write closes the peer. That
		// return is therefore this carrier's write hook: it reports a written
		// Observe 0 response, which opens the watch's ready gate.
		options.WithProcessReceivedMessageFunc(func(req *pool.Message, cc *coapclient.Conn, handler config.HandlerFunc[*coapclient.Conn]) {
			peer := lookup(cc)
			if peer == nil {
				cc.ProcessReceivedMessageWithHandler(req, handler)
				return
			}
			var token [8]byte
			ready := 0
			cc.ProcessReceivedMessageWithHandler(req, func(w *responsewriter.ResponseWriter[*coapclient.Conn], r *pool.Message) {
				handler(w, r)
				if response := w.Message(); response.IsModified() && response.Code() == codes.Content && len(response.Token()) <= len(token) {
					if observe, err := response.Observe(); err == nil && observe == 0 {
						ready = copy(token[:], response.Token())
					}
				}
			})
			if ready > 0 && cc.Context().Err() == nil {
				peer.state.wrote(token[:ready], codes.Content, true, nil)
			}
		}),
		options.WithHandlerFunc(func(w *responsewriter.ResponseWriter[*coapclient.Conn], req *pool.Message) {
			peer := lookup(w.Conn())
			if peer == nil || !peer.state.beginHandler() {
				_ = w.SetResponse(codes.ServiceUnavailable, 0, nil)
				return
			}
			defer peer.state.endHandler()
			serveCoAPRequest(ctx, w, w.Conn(), req, d, peer.state)
		}),
	)
	l := coapNet.NewUDPConn("udp", conn)
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { server.Stop(); close(stopped) })
	err := server.Serve(l)
	cancel()
	if !stop() {
		<-stopped
	}
	_ = l.Close()
	work.Wait()
	if parent.Err() != nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

type coapUDPPeer struct {
	state  *coapState
	ledger *coapLedger
}
