//go:build linux

package bluezgatt

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	objectManagerInterface = "org.freedesktop.DBus.ObjectManager"
	propertiesInterface    = "org.freedesktop.DBus.Properties"
	introspectInterface    = "org.freedesktop.DBus.Introspectable"
	characteristicIface    = "org.bluez.GattCharacteristic1"
	serviceIface           = "org.bluez.GattService1"
)

type callGate struct {
	mu     sync.Mutex
	closed bool
	active chan struct{}
	wg     sync.WaitGroup
}

func newCallGate(limit int) *callGate { return &callGate{active: make(chan struct{}, limit)} }

func (g *callGate) acquire() *callTicket {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	select {
	case g.active <- struct{}{}:
		g.wg.Add(1)
		return &callTicket{gate: g}
	default:
		return nil
	}
}

func (g *callGate) close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

func (g *callGate) wait() { g.wg.Wait() }

type callTicket struct {
	gate *callGate
	once sync.Once
}

func (t *callTicket) release() {
	if t == nil || t.gate == nil {
		return
	}
	t.once.Do(func() {
		<-t.gate.active
		t.gate.wg.Done()
	})
}

type gattHandler struct{ state *serverState }

func (h *gattHandler) LookupObject(path dbus.ObjectPath) (dbus.ServerObject, bool) {
	properties := h.state.properties(path)
	if properties == nil && path != gattRoot {
		return nil, false
	}
	interfaces := map[string]dbus.Interface{
		propertiesInterface: &gattInterface{methods: map[string]dbus.Method{
			"Get":    h.state.method([]reflect.Type{reflect.TypeOf(""), reflect.TypeOf("")}, 1, propertiesGet(path)),
			"GetAll": h.state.method([]reflect.Type{reflect.TypeOf("")}, 1, propertiesGetAll(path)),
		}},
		introspectInterface: &gattInterface{methods: map[string]dbus.Method{
			"Introspect": h.state.method(nil, 1, func([]any) ([]any, error) { return []any{introspectionXML(path)}, nil }),
		}},
	}
	if path == gattRoot {
		interfaces[objectManagerInterface] = &gattInterface{methods: map[string]dbus.Method{
			"GetManagedObjects": h.state.method(nil, 1, func([]any) ([]any, error) { return []any{managedObjects()}, nil }),
		}}
	}
	if path == gattRequest {
		interfaces[characteristicIface] = &gattInterface{methods: map[string]dbus.Method{
			"WriteValue": h.state.method([]reflect.Type{reflect.TypeOf([]byte{}), reflect.TypeOf(map[string]dbus.Variant{})}, 0, h.state.writeValue),
		}}
	}
	if path == gattResult {
		interfaces[characteristicIface] = &gattInterface{methods: map[string]dbus.Method{
			"ReadValue": h.state.method([]reflect.Type{reflect.TypeOf(map[string]dbus.Variant{})}, 1, h.state.readValue),
		}}
	}
	if path != gattRoot && path != gattService && path != gattRequest && path != gattResult {
		return nil, false
	}
	return &gattObject{interfaces: interfaces}, true
}

type gattObject struct{ interfaces map[string]dbus.Interface }

func (o *gattObject) LookupInterface(name string) (dbus.Interface, bool) {
	interfaceValue, ok := o.interfaces[name]
	return interfaceValue, ok
}

type gattInterface struct{ methods map[string]dbus.Method }

func (i *gattInterface) LookupMethod(name string) (dbus.Method, bool) {
	method, ok := i.methods[name]
	return method, ok
}

type gattMethod struct {
	argTypes    []reflect.Type
	returnCount int
	call        func([]any) ([]any, error)
}

func (s *serverState) method(types []reflect.Type, returnCount int, call func([]any) ([]any, error)) dbus.Method {
	return &gattMethod{argTypes: types, returnCount: returnCount, call: call}
}

func (m *gattMethod) DecodeArguments(_ *dbus.Conn, _ string, _ *dbus.Message, body []any) ([]any, error) {
	if len(body) == 0 {
		return nil, invalidArgs()
	}
	ticket, ok := body[len(body)-1].(*callTicket)
	if !ok || ticket == nil {
		return nil, invalidArgs()
	}
	values := body[:len(body)-1]
	if len(values) != len(m.argTypes) {
		ticket.release()
		return nil, invalidArgs()
	}
	decoded := make([]any, len(m.argTypes))
	if len(values) != 0 {
		pointers := make([]any, len(values))
		for i, typ := range m.argTypes {
			pointers[i] = reflect.New(typ).Interface()
		}
		if err := dbus.Store(values, pointers...); err != nil {
			ticket.release()
			return nil, invalidArgs()
		}
		copy(decoded, pointers)
	}
	return append(decoded, ticket), nil
}

func (m *gattMethod) Call(args ...any) ([]any, error) {
	if len(args) == 0 {
		return nil, invalidArgs()
	}
	ticket, ok := args[len(args)-1].(*callTicket)
	if !ok || ticket == nil {
		return nil, invalidArgs()
	}
	defer ticket.release()
	if len(args) != len(m.argTypes)+1 {
		return nil, invalidArgs()
	}
	values := make([]any, len(m.argTypes))
	for i := range values {
		value := reflect.ValueOf(args[i])
		if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
			return nil, invalidArgs()
		}
		values[i] = value.Elem().Interface()
	}
	return m.call(values)
}

func (m *gattMethod) NumArguments() int { return len(m.argTypes) }
func (m *gattMethod) NumReturns() int   { return m.returnCount }
func (m *gattMethod) ArgumentValue(i int) any {
	if i < 0 || i >= len(m.argTypes) {
		return nil
	}
	return reflect.Zero(m.argTypes[i]).Interface()
}
func (m *gattMethod) ReturnValue(int) any { return nil }

func propertiesGet(path dbus.ObjectPath) func([]any) ([]any, error) {
	return func(args []any) ([]any, error) {
		if len(args) != 2 {
			return nil, invalidArgs()
		}
		iface, ok1 := args[0].(string)
		name, ok2 := args[1].(string)
		if !ok1 || !ok2 {
			return nil, invalidArgs()
		}
		properties := propertiesForPath(path)
		values, ok := properties[iface]
		if !ok {
			return nil, notPermitted()
		}
		value, ok := values[name]
		if !ok {
			return nil, notPermitted()
		}
		return []any{dbus.MakeVariant(value)}, nil
	}
}

func propertiesGetAll(path dbus.ObjectPath) func([]any) ([]any, error) {
	return func(args []any) ([]any, error) {
		if len(args) != 1 {
			return nil, invalidArgs()
		}
		iface, ok := args[0].(string)
		if !ok {
			return nil, invalidArgs()
		}
		values, ok := propertiesForPath(path)[iface]
		if !ok {
			return nil, notPermitted()
		}
		result := make(map[string]dbus.Variant, len(values))
		for key, value := range values {
			result[key] = dbus.MakeVariant(value)
		}
		return []any{result}, nil
	}
}

func (s *serverState) properties(path dbus.ObjectPath) map[string]map[string]any {
	return propertiesForPath(path)
}

func propertiesForPath(path dbus.ObjectPath) map[string]map[string]any {
	switch path {
	case gattService:
		return map[string]map[string]any{serviceIface: {
			"UUID": serviceUUID, "Primary": true, "Includes": []dbus.ObjectPath{},
		}}
	case gattRequest:
		return map[string]map[string]any{characteristicIface: {
			"UUID": requestUUID, "Service": gattService,
			"Flags": []string{"write", "encrypt-authenticated-write"},
		}}
	case gattResult:
		return map[string]map[string]any{characteristicIface: {
			"UUID": resultUUID, "Service": gattService,
			"Flags": []string{"read", "encrypt-authenticated-read"},
		}}
	case gattRoot:
		return map[string]map[string]any{}
	default:
		return nil
	}
}

func managedObjects() map[dbus.ObjectPath]map[string]map[string]dbus.Variant {
	objects := make(map[dbus.ObjectPath]map[string]map[string]dbus.Variant, 3)
	for _, path := range []dbus.ObjectPath{gattService, gattRequest, gattResult} {
		interfaces := propertiesForPath(path)
		converted := make(map[string]map[string]dbus.Variant, len(interfaces))
		for iface, values := range interfaces {
			props := make(map[string]dbus.Variant, len(values))
			for key, value := range values {
				props[key] = dbus.MakeVariant(value)
			}
			converted[iface] = props
		}
		objects[path] = converted
	}
	return objects
}

func introspectionXML(path dbus.ObjectPath) string {
	switch path {
	case gattRoot:
		return `<node><interface name="org.freedesktop.DBus.ObjectManager"><method name="GetManagedObjects"><arg type="a{oa{sa{sv}}}" direction="out"/></method></interface><interface name="org.freedesktop.DBus.Introspectable"><method name="Introspect"><arg type="s" direction="out"/></method></interface></node>`
	case gattService:
		return `<node><node name="char0"/><node name="char1"/><interface name="org.freedesktop.DBus.Properties"><method name="Get"><arg type="s" direction="in"/><arg type="s" direction="in"/><arg type="v" direction="out"/></method><method name="GetAll"><arg type="s" direction="in"/><arg type="a{sv}" direction="out"/></method></interface></node>`
	case gattRequest:
		return `<node><interface name="org.bluez.GattCharacteristic1"><method name="WriteValue"><arg type="ay" direction="in"/><arg type="a{sv}" direction="in"/></method></interface><interface name="org.freedesktop.DBus.Properties"><method name="Get"><arg type="s" direction="in"/><arg type="s" direction="in"/><arg type="v" direction="out"/></method><method name="GetAll"><arg type="s" direction="in"/><arg type="a{sv}" direction="out"/></method></interface></node>`
	case gattResult:
		return `<node><interface name="org.bluez.GattCharacteristic1"><method name="ReadValue"><arg type="a{sv}" direction="in"/><arg type="ay" direction="out"/></method></interface><interface name="org.freedesktop.DBus.Properties"><method name="Get"><arg type="s" direction="in"/><arg type="s" direction="in"/><arg type="v" direction="out"/></method><method name="GetAll"><arg type="s" direction="in"/><arg type="a{sv}" direction="out"/></method></interface></node>`
	default:
		return `<node/>`
	}
}

func (s *serverState) writeValue(args []any) ([]any, error) {
	if len(args) != 2 || !s.active.Load() {
		return nil, notPermitted()
	}
	value, ok := args[0].([]byte)
	options, optionsOK := args[1].(map[string]dbus.Variant)
	if !ok || !optionsOK {
		return nil, invalidArgs()
	}
	peer, mtu, _, err := s.verifyDevice(options, true)
	if err != nil {
		return nil, notPermitted()
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	s.sessions.Reap(time.Now())
	if err := s.sessions.Write(ctx, peer, mtu, value); err != nil {
		return nil, notPermitted()
	}
	return nil, nil
}

func (s *serverState) readValue(args []any) ([]any, error) {
	if len(args) != 1 || !s.active.Load() {
		return nil, notPermitted()
	}
	options, ok := args[0].(map[string]dbus.Variant)
	if !ok {
		return nil, invalidArgs()
	}
	peer, mtu, offset, err := s.verifyDevice(options, false)
	if err != nil {
		return nil, notPermitted()
	}
	s.sessions.Reap(time.Now())
	value, err := s.sessions.Read(peer, mtu, offset)
	if err != nil {
		return nil, notPermitted()
	}
	return []any{value}, nil
}

func (s *serverState) verifyDevice(options map[string]dbus.Variant, writing bool) (Peer, uint16, uint16, error) {
	allowed := map[string]bool{"device": true, "link": true, "mtu": true, "offset": true}
	if writing {
		allowed["type"] = true
		allowed["prepare-authorize"] = true
	}
	for key := range options {
		if !allowed[key] {
			return Peer{}, 0, 0, errors.New("unsupported GATT operation option")
		}
	}
	deviceValue, deviceOK := variantValue(options["device"])
	device, devicePathOK := deviceValue.(dbus.ObjectPath)
	link, linkOK := variantString(options["link"])
	mtuValue, mtuOK := variantValue(options["mtu"])
	mtu, mtuTypeOK := mtuValue.(uint16)
	if !deviceOK || !devicePathOK || link == "" || !linkOK || link != "LE" || !mtuOK || !mtuTypeOK || mtu < 23 || mtu > maxATTMTU {
		return Peer{}, 0, 0, errors.New("invalid LE GATT device options")
	}
	var offset uint16
	if value, exists := options["offset"]; exists {
		offsetValue := value.Value()
		var valid bool
		offset, valid = offsetValue.(uint16)
		if !valid {
			return Peer{}, 0, 0, errors.New("invalid GATT value offset")
		}
	}
	if writing && offset != 0 {
		return Peer{}, 0, 0, errors.New("offset writes are unsupported")
	}
	if writing {
		if value, exists := options["type"]; exists {
			typeValue, valid := value.Value().(string)
			if !valid || typeValue != "request" {
				return Peer{}, 0, 0, errors.New("only acknowledged GATT writes are supported")
			}
		}
		if value, exists := options["prepare-authorize"]; exists {
			prepare, valid := value.Value().(bool)
			if !valid || prepare {
				return Peer{}, 0, 0, errors.New("prepared GATT writes are unsupported")
			}
		}
	}
	peer, configured := s.devicePeer[device]
	if !configured || device != deviceObjectPath(s.adapter, peer.Address) {
		return Peer{}, 0, 0, errors.New("unknown GATT device path")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	properties, err := s.blueZProperties(ctx, device, "org.bluez.Device1")
	if err != nil {
		return Peer{}, 0, 0, err
	}
	address, addressOK := variantString(properties["Address"])
	addressType, typeOK := variantString(properties["AddressType"])
	adapterValue, adapterOK := variantValue(properties["Adapter"])
	adapter, adapterTypeOK := adapterValue.(dbus.ObjectPath)
	paired, pairedOK := variantBool(properties["Paired"])
	connected, connectedOK := variantBool(properties["Connected"])
	if !addressOK || !strings.EqualFold(address, peer.Address) || !typeOK || addressType != peer.AddressType || !adapterOK || !adapterTypeOK || adapter != s.adapter || !pairedOK || !paired || !connectedOK || !connected {
		return Peer{}, 0, 0, errors.New("BlueZ device state is not eligible")
	}
	return peer, mtu, offset, nil
}

func variantValue(value dbus.Variant) (any, bool) {
	if value.Signature().String() == "" {
		return nil, false
	}
	return value.Value(), true
}

func variantString(value dbus.Variant) (string, bool) {
	if value.Signature().String() == "" {
		return "", false
	}
	text, ok := value.Value().(string)
	return text, ok
}

func variantBool(value dbus.Variant) (bool, bool) {
	if value.Signature().String() == "" {
		return false, false
	}
	flag, ok := value.Value().(bool)
	return flag, ok
}

func invalidArgs() error {
	return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{"Invalid arguments"})
}

func notPermitted() error {
	return dbus.NewError("org.bluez.Error.NotPermitted", []any{"Operation not permitted"})
}

var _ dbus.Handler = (*gattHandler)(nil)
var _ dbus.ServerObject = (*gattObject)(nil)
var _ dbus.Interface = (*gattInterface)(nil)
var _ dbus.Method = (*gattMethod)(nil)
