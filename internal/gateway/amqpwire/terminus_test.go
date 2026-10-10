package amqpwire

import "testing"

func TestTransportTerminusAndStateValidation(t *testing.T) {
	described := func(code uint64, fields ...Value) Value {
		return Value{Descriptor: &Value{Code: 0x80, Number: code}, Children: []Value{{Code: 0xc0, Children: fields}}}
	}
	for _, code := range []uint64{0x28, 0x29} {
		v := described(code, Value{Code: 0xa1, Data: []byte("inbox")})
		if !validTerminus(v, code == 0x28) {
			t.Fatalf("valid terminus %x", code)
		}
		v.Children[0].Children = append(v.Children[0].Children, Value{Code: 0x70, Number: 3})
		if validTerminus(v, code == 0x28) {
			t.Fatal("invalid durability")
		}
	}
	for _, v := range []Value{
		described(0x23, Value{Code: 0x70}, Value{Code: 0x80}),
		described(0x24), described(0x25), described(0x26),
		described(0x27, Value{Code: 0x40}, Value{Code: 0x40}, Value{Code: 0xc1, Children: []Value{{Code: 0x80, Number: 1}, {Code: 0x40}}}),
	} {
		if !validDeliveryState(v) {
			t.Fatalf("valid state %+v", v)
		}
	}
	for _, v := range []Value{
		described(0x23), described(0x24, Value{Code: 0x40}), described(0x30),
		described(0x25, Value{Code: 0xa1}), described(0x27, Value{Code: 0x70}),
	} {
		if validDeliveryState(v) {
			t.Fatalf("invalid state %+v", v)
		}
	}
	fields := []Value{{Code: 0x40}, {Code: 0x40}, {Code: 0x40}, {Code: 0x40}, {Code: 0x40}, {Code: 0x40}, {Code: 0x40}, {Code: 0x40}, described(0x23, Value{Code: 0x70}, Value{Code: 0x80})}
	if validTerminus(described(0x28, fields...), true) {
		t.Fatal("received is not default outcome")
	}
	fields[8] = described(0x26)
	if !validTerminus(described(0x28, fields...), true) {
		t.Fatal("released default outcome")
	}
	fields[5] = Value{Code: 0xc1}
	if validTerminus(described(0x28, fields...), true) {
		t.Fatal("dynamic properties without dynamic")
	}
}

func TestTransportTypedNestedFields(t *testing.T) {
	state := Value{Descriptor: &Value{Code: 0x80, Number: 0x24}, Children: []Value{{Code: 0xc0}}}
	unsettled := Value{Code: 0xc1, Children: []Value{{Code: 0xa0, Data: []byte("tag")}, state}}
	if !typedField(unsettled, unsettledType) {
		t.Fatal("valid unsettled map")
	}
	unsettled.Children[0].Data = make([]byte, 33)
	if typedField(unsettled, unsettledType) {
		t.Fatal("long unsettled tag")
	}
	properties := Value{Code: 0xc1, Children: []Value{{Code: 0xa3, Data: []byte("name")}, {Code: 0x40}}}
	if !typedField(properties, fieldsType) {
		t.Fatal("valid fields")
	}
	properties.Children[0].Code = 0xa1
	if typedField(properties, fieldsType) {
		t.Fatal("string fields key")
	}
}
