//go:build linux

package bluezgatt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const (
	systemBusSocket = "/run/dbus/system_bus_socket"
	bluezName       = "org.bluez"
	dbusName        = "org.freedesktop.DBus"
	gattRoot        = dbus.ObjectPath("/org/airc/gateway/gatt")
	gattService     = dbus.ObjectPath("/org/airc/gateway/gatt/service0")
	gattRequest     = dbus.ObjectPath("/org/airc/gateway/gatt/service0/char0")
	gattResult      = dbus.ObjectPath("/org/airc/gateway/gatt/service0/char1")
	serviceUUID     = "c74de000-3bcd-4efb-9d4d-860ab6927e80"
	requestUUID     = "c74de001-3bcd-4efb-9d4d-860ab6927e80"
	resultUUID      = "c74de002-3bcd-4efb-9d4d-860ab6927e80"
	maxPeers        = 16
	maxBusCalls     = 8
	maxATTMTU       = 517
)

type serverState struct {
	conn       *dbus.Conn
	transport  *boundedBusConn
	sessions   *Sessions
	adapter    dbus.ObjectPath
	adapterMAC string
	peers      map[string]Peer
	devicePeer map[dbus.ObjectPath]Peer
	owner      atomic.Value
	active     atomic.Bool
	ctx        context.Context
	cancel     context.CancelFunc
	failureMu  sync.Mutex
	failure    error
	registered atomic.Bool
	signals    chan *dbus.Signal
	watchCtx   context.Context
	watchStop  context.CancelFunc
	watchWG    sync.WaitGroup
	calls      *callGate
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
	runOnce    atomic.Bool
}

// Start connects to the root-owned system bus, pins the current BlueZ owner,
// and registers a fixed two-characteristic GATT application. Transfer methods
// stay inactive until Run is called.
func Start(ctx context.Context, adapterPath, adapterAddress string, peers []Peer, handler Handler, slots chan struct{}) (*Server, error) {
	if ctx == nil || handler == nil || slots == nil {
		return nil, errors.New("BlueZ GATT requires a context, handler and request slots")
	}
	adapter := dbus.ObjectPath(adapterPath)
	if !adapter.IsValid() || adapter == gattRoot || !strings.HasPrefix(adapterPath, "/org/bluez/") {
		return nil, errors.New("invalid BlueZ adapter object path")
	}
	adapterMAC, err := canonicalBDAddr(adapterAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid BlueZ adapter address: %w", err)
	}
	peerMap, devicePeer, err := validatePeers(peers, adapter)
	if err != nil {
		return nil, err
	}

	startCtx, cancelStart := context.WithTimeout(ctx, 5*time.Second)
	defer cancelStart()
	dialer := net.Dialer{}
	netConn, err := dialer.DialContext(startCtx, "unix", systemBusSocket)
	if err != nil {
		return nil, fmt.Errorf("connect to system bus: %w", err)
	}
	closeOnCancel := context.AfterFunc(startCtx, func() { _ = netConn.Close() })
	transport := &boundedBusConn{conn: netConn}
	transport.setStartupDeadline(time.Now().Add(5 * time.Second))
	if err := netConn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		_ = transport.Close()
		closeOnCancel()
		return nil, fmt.Errorf("bound system bus startup: %w", err)
	}
	if err := requireRootSystemBusPeer(netConn); err != nil {
		_ = transport.Close()
		closeOnCancel()
		return nil, err
	}
	if ctx.Err() != nil {
		_ = transport.Close()
		closeOnCancel()
		return nil, ctx.Err()
	}

	stateCtx, cancelState := context.WithCancel(context.Background())
	state := &serverState{
		transport: transport, adapter: adapter, adapterMAC: adapterMAC,
		peers: peerMap, devicePeer: devicePeer, ctx: stateCtx, cancel: cancelState,
		signals: make(chan *dbus.Signal, 32), calls: newCallGate(maxBusCalls),
		closeDone: make(chan struct{}),
	}
	state.watchCtx, state.watchStop = context.WithCancel(context.Background())
	state.sessions, err = NewSessions(len(peers), slots, handler)
	if err != nil {
		cancelState()
		_ = transport.Close()
		closeOnCancel()
		return nil, fmt.Errorf("create GATT transfer sessions: %w", err)
	}
	state.conn, err = dbus.NewConn(transport,
		dbus.WithAuth(dbus.AuthExternal(strconv.Itoa(os.Geteuid()))),
		dbus.WithHandler(&gattHandler{state: state}),
		dbus.WithIncomingInterceptor(state.intercept),
	)
	if err != nil {
		_ = state.sessions.Close()
		cancelState()
		_ = transport.Close()
		closeOnCancel()
		return nil, fmt.Errorf("create system bus connection: %w", err)
	}
	if err := state.conn.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}); err != nil {
		_ = state.conn.Close()
		_ = state.sessions.Close()
		cancelState()
		closeOnCancel()
		return nil, fmt.Errorf("authenticate to system bus: %w", err)
	}
	if err := state.conn.Hello(); err != nil {
		_ = state.conn.Close()
		_ = state.sessions.Close()
		cancelState()
		closeOnCancel()
		return nil, fmt.Errorf("identify system bus connection: %w", err)
	}
	if !closeOnCancel() {
		_ = state.conn.Close()
		_ = state.sessions.Close()
		cancelState()
		return nil, startCtx.Err()
	}
	if err := startCtx.Err(); err != nil {
		_ = state.conn.Close()
		_ = state.sessions.Close()
		cancelState()
		return nil, err
	}

	state.conn.Signal(state.signals)
	if err := state.conn.AddMatchSignalContext(startCtx,
		dbus.WithMatchSender(dbusName),
		dbus.WithMatchInterface(dbusName),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, bluezName),
	); err != nil {
		state.conn.RemoveSignal(state.signals)
		_ = state.conn.Close()
		_ = state.sessions.Close()
		cancelState()
		return nil, fmt.Errorf("watch BlueZ ownership: %w", err)
	}
	var owner string
	if err := state.conn.BusObject().CallWithContext(startCtx, "org.freedesktop.DBus.GetNameOwner", 0, bluezName).Store(&owner); err != nil || !validUniqueName(owner) {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = state.conn.RemoveMatchSignalContext(cleanupCtx, dbus.WithMatchSender(dbusName), dbus.WithMatchInterface(dbusName), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, bluezName))
		cleanupCancel()
		state.conn.RemoveSignal(state.signals)
		_ = state.conn.Close()
		_ = state.sessions.Close()
		cancelState()
		if err == nil {
			err = errors.New("BlueZ returned an invalid unique owner")
		}
		return nil, fmt.Errorf("find BlueZ owner: %w", err)
	}
	if err := state.verifyBlueZUID(startCtx, owner); err != nil {
		_ = state.Close()
		return nil, err
	}
	state.owner.Store(owner)
	state.watchWG.Add(1)
	go state.watchBlueZ()
	if err := state.verifyAdapter(startCtx); err != nil {
		_ = state.Close()
		return nil, err
	}
	if err := state.ctx.Err(); err != nil {
		_ = state.Close()
		return nil, fmt.Errorf("BlueZ changed during GATT startup: %w", err)
	}
	state.registered.Store(true)
	if err := state.ownerCall(startCtx, owner, adapter, "org.bluez.GattManager1.RegisterApplication", gattRoot, map[string]dbus.Variant{}).Err; err != nil {
		_ = state.Close()
		return nil, fmt.Errorf("register BlueZ GATT application: %w", err)
	}
	if err := state.ctx.Err(); err != nil {
		_ = state.Close()
		return nil, fmt.Errorf("BlueZ changed during GATT registration: %w", err)
	}
	if err := transport.finishStartup(); err != nil {
		_ = state.Close()
		return nil, fmt.Errorf("finish bounded system bus startup: %w", err)
	}
	return &Server{run: state.run, close: state.Close}, nil
}

func requireRootSystemBusPeer(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("system bus connection is not a Unix socket")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("inspect system bus peer: %w", err)
	}
	var credential *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credential, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("inspect system bus peer credentials: %w", err)
	}
	if controlErr != nil {
		return fmt.Errorf("read system bus peer credentials: %w", controlErr)
	}
	if credential == nil || credential.Uid != 0 {
		return errors.New("system bus socket peer is not root")
	}
	return nil
}

func validatePeers(peers []Peer, adapter dbus.ObjectPath) (map[string]Peer, map[dbus.ObjectPath]Peer, error) {
	if len(peers) == 0 || len(peers) > maxPeers {
		return nil, nil, errors.New("BlueZ GATT peer count is outside limits")
	}
	byKey := make(map[string]Peer, len(peers))
	byPath := make(map[dbus.ObjectPath]Peer, len(peers))
	for _, peer := range peers {
		address, err := canonicalBDAddr(peer.Address)
		if err != nil || (peer.AddressType != "public" && peer.AddressType != "random") {
			return nil, nil, errors.New("invalid BlueZ LE peer address or address type")
		}
		peer.Address = address
		key := peerKey(peer)
		path := deviceObjectPath(adapter, peer.Address)
		if _, exists := byKey[key]; exists {
			return nil, nil, errors.New("duplicate BlueZ LE peer")
		}
		if _, exists := byPath[path]; exists {
			return nil, nil, errors.New("BlueZ device paths cannot distinguish configured address types")
		}
		byKey[key], byPath[path] = peer, peer
	}
	return byKey, byPath, nil
}

func canonicalBDAddr(value string) (string, error) {
	address, err := config.ParseBluetoothAddress(strings.ToLower(value))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", address[0], address[1], address[2], address[3], address[4], address[5]), nil
}

func peerKey(peer Peer) string { return peer.AddressType + "\x00" + peer.Address }

func deviceObjectPath(adapter dbus.ObjectPath, address string) dbus.ObjectPath {
	return dbus.ObjectPath(string(adapter) + "/dev_" + strings.ReplaceAll(strings.ToUpper(address), ":", "_"))
}

func validUniqueName(name string) bool {
	return strings.HasPrefix(name, ":") && len(name) > 1 && !strings.ContainsAny(name, " \t\r\n")
}

func (s *serverState) verifyBlueZUID(ctx context.Context, owner string) error {
	var uid uint32
	if err := s.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixUser", 0, owner).Store(&uid); err != nil {
		return fmt.Errorf("verify BlueZ daemon identity: %w", err)
	}
	if uid != 0 {
		return errors.New("BlueZ daemon is not root-owned")
	}
	return nil
}

func (s *serverState) verifyAdapter(ctx context.Context) error {
	props, err := s.blueZProperties(ctx, s.adapter, "org.bluez.Adapter1")
	if err != nil {
		return fmt.Errorf("read BlueZ adapter properties: %w", err)
	}
	address, addressOK := variantString(props["Address"])
	powered, powerOK := variantBool(props["Powered"])
	if !addressOK || !strings.EqualFold(address, s.adapterMAC) || !powerOK || !powered {
		return errors.New("BlueZ adapter address or power state does not match configuration")
	}
	return nil
}

func (s *serverState) blueZProperties(ctx context.Context, path dbus.ObjectPath, iface string) (map[string]dbus.Variant, error) {
	owner := s.currentOwner()
	if owner == "" {
		return nil, errors.New("BlueZ owner is unavailable")
	}
	var properties map[string]dbus.Variant
	call := s.conn.Object(owner, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, iface)
	if err := call.Store(&properties); err != nil {
		return nil, err
	}
	return properties, nil
}

func (s *serverState) ownerCall(ctx context.Context, owner string, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	if owner == "" || owner != s.currentOwner() {
		return &dbus.Call{Err: errors.New("BlueZ owner changed")}
	}
	return s.conn.Object(owner, path).CallWithContext(ctx, method, 0, args...)
}

func (s *serverState) currentOwner() string {
	value := s.owner.Load()
	if value == nil {
		return ""
	}
	return value.(string)
}

func (s *serverState) watchBlueZ() {
	defer s.watchWG.Done()
	for {
		select {
		case <-s.watchCtx.Done():
			return
		case <-s.conn.Context().Done():
			s.fail(errors.New("system bus disconnected"))
			return
		case signal := <-s.signals:
			if signal == nil || signal.Sender != dbusName || signal.Path != dbus.ObjectPath("/org/freedesktop/DBus") || signal.Name != dbusName+".NameOwnerChanged" || len(signal.Body) != 3 {
				continue
			}
			name, nameOK := signal.Body[0].(string)
			_, oldOK := signal.Body[1].(string)
			newOwner, newOK := signal.Body[2].(string)
			if !nameOK || !oldOK || !newOK || name != bluezName {
				continue
			}
			if newOwner != s.currentOwner() {
				s.fail(errors.New("BlueZ daemon owner changed"))
				return
			}
		}
	}
}

func (s *serverState) fail(err error) {
	s.failureMu.Lock()
	if s.failure == nil {
		s.failure = err
	}
	s.failureMu.Unlock()
	s.cancel()
}

func (s *serverState) intercept(message *dbus.Message) {
	if message == nil {
		return
	}
	sender := headerString(message, dbus.FieldSender)
	switch message.Type {
	case dbus.TypeMethodCall:
		if sender == "" || sender != s.currentOwner() || !s.allowedMethod(message) {
			message.Type = dbus.Type(0)
			return
		}
		if fds, ok := message.Headers[dbus.FieldUnixFDs]; ok {
			count, valid := fds.Value().(uint32)
			if !valid || count != 0 {
				message.Type = dbus.Type(0)
				return
			}
		}
		ticket := s.calls.acquire()
		if ticket == nil {
			message.Type = dbus.Type(0)
			return
		}
		message.Body = append(message.Body, ticket)
	case dbus.TypeMethodReply, dbus.TypeError:
		if sender != dbusName && (sender == "" || sender != s.currentOwner()) {
			message.Type = dbus.Type(0)
		}
	case dbus.TypeSignal:
		path, pathOK := message.Headers[dbus.FieldPath]
		if sender != dbusName || !pathOK || path.Value() != dbus.ObjectPath("/org/freedesktop/DBus") ||
			headerString(message, dbus.FieldInterface) != dbusName || headerString(message, dbus.FieldMember) != "NameOwnerChanged" {
			message.Type = dbus.Type(0)
		}
	default:
		message.Type = dbus.Type(0)
	}
}

func headerString(message *dbus.Message, field dbus.HeaderField) string {
	variant, ok := message.Headers[field]
	if !ok || variant.Signature().String() == "" {
		return ""
	}
	value, _ := variant.Value().(string)
	return value
}

func (s *serverState) allowedMethod(message *dbus.Message) bool {
	pathHeader, pathPresent := message.Headers[dbus.FieldPath]
	ifaceHeader, ifacePresent := message.Headers[dbus.FieldInterface]
	memberHeader, memberPresent := message.Headers[dbus.FieldMember]
	if !pathPresent || !ifacePresent || !memberPresent {
		return false
	}
	path, pathOK := pathHeader.Value().(dbus.ObjectPath)
	iface, ifaceOK := ifaceHeader.Value().(string)
	member, memberOK := memberHeader.Value().(string)
	if !pathOK || !ifaceOK || !memberOK {
		return false
	}
	switch iface {
	case "org.freedesktop.DBus.ObjectManager":
		return path == gattRoot && member == "GetManagedObjects"
	case "org.freedesktop.DBus.Properties":
		return (path == gattRoot || path == gattService || path == gattRequest || path == gattResult) && (member == "Get" || member == "GetAll")
	case "org.freedesktop.DBus.Introspectable":
		return (path == gattRoot || path == gattService || path == gattRequest || path == gattResult) && member == "Introspect"
	case "org.bluez.GattCharacteristic1":
		return (path == gattRequest && member == "WriteValue") || (path == gattResult && member == "ReadValue")
	default:
		return false
	}
}

func (s *serverState) run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("BlueZ GATT run context is nil")
	}
	if !s.runOnce.CompareAndSwap(false, true) {
		return errors.New("BlueZ GATT server can only run once")
	}
	if err := s.ctx.Err(); err != nil {
		_ = s.Close()
		return err
	}
	s.active.Store(true)
	reap := time.NewTicker(time.Second)
	defer reap.Stop()
	for {
		select {
		case <-ctx.Done():
			goto finished
		case <-s.ctx.Done():
			goto finished
		case <-s.conn.Context().Done():
			s.fail(errors.New("system bus disconnected"))
			goto finished
		case <-reap.C:
			s.sessions.Reap(time.Now())
		}
	}

finished:
	s.active.Store(false)
	closeErr := s.Close()
	if ctx.Err() == nil {
		s.failureMu.Lock()
		failure := s.failure
		s.failureMu.Unlock()
		if failure != nil {
			return errors.Join(failure, closeErr)
		}
	}
	return closeErr
}

func (s *serverState) Close() error {
	s.closeOnce.Do(func() {
		defer close(s.closeDone)
		s.active.Store(false)
		s.calls.close()
		s.cancel()
		if s.registered.Load() && s.conn != nil && s.conn.Connected() && s.currentOwner() != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			call := s.ownerCall(ctx, s.currentOwner(), s.adapter, "org.bluez.GattManager1.UnregisterApplication", gattRoot)
			s.closeErr = errors.Join(s.closeErr, call.Err)
			cancel()
		}
		if s.conn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			s.closeErr = errors.Join(s.closeErr, s.conn.RemoveMatchSignalContext(ctx,
				dbus.WithMatchSender(dbusName), dbus.WithMatchInterface(dbusName),
				dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, bluezName)))
			cancel()
			s.conn.RemoveSignal(s.signals)
			s.watchStop()
			s.closeErr = errors.Join(s.closeErr, s.conn.Close())
		}
		s.watchStop()
		s.watchWG.Wait()
		s.calls.wait()
		if s.sessions != nil {
			s.closeErr = errors.Join(s.closeErr, s.sessions.Close())
		}
	})
	<-s.closeDone
	return s.closeErr
}
