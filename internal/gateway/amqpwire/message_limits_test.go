package amqpwire

import "testing"

func TestMessageAggregateBodyLimit(t *testing.T) {
	part := make([]byte, 8+MaxMessageBody/2)
	copy(part, []byte{0, 0x53, 0x75, 0xb0, 0, 0, 0x40, 0})
	wire := append(append([]byte{}, part...), part...)
	if _, err := DecodeMessage(wire); err != nil {
		t.Fatal(err)
	}
	wire = append(wire, 0, 0x53, 0x75, 0xa0, 1, 'x')
	if _, err := DecodeMessage(wire); err == nil {
		t.Fatal("aggregate body limit")
	}
}

func TestMessageSharesNodeBudgetAcrossSections(t *testing.T) {
	annotation := func(code uint64, pairs int) []byte {
		fields := make([]Value, 0, pairs*2)
		for i := 0; i < pairs; i++ {
			fields = append(fields, Value{Code: 0x80, Number: uint64(i)}, Value{Code: 0x40})
		}
		wire, err := EncodeValue(Value{Descriptor: &Value{Code: 0x80, Number: code}, Children: []Value{{Code: 0xc1, Children: fields}}}, MaxFrameBytes)
		if err != nil {
			t.Fatal(err)
		}
		return wire
	}
	for _, pairs := range []int{509, 510} {
		wire := append(annotation(0x71, pairs), annotation(0x72, 510)...)
		wire = append(wire, 0, 0x53, 0x75, 0xa0, 1, 'x')
		_, err := DecodeMessage(wire)
		if (pairs == 509) != (err == nil) {
			t.Fatalf("pairs=%d err=%v", pairs, err)
		}
	}
}

func TestMessageRejectedAnnotationRequiresTypedError(t *testing.T) {
	for _, value := range []Value{
		{Code: 0x40},
		{Descriptor: &Value{Code: 0x80, Number: 0x1d}, Children: []Value{{Code: 0xc0}}},
		{Descriptor: &Value{Code: 0x80, Number: 0x1d}, Children: []Value{{Code: 0xc0, Children: []Value{{Code: 0xa3, Data: []byte("amqp:unauthorized-access")}}}}},
	} {
		wire, err := EncodeValue(Value{Descriptor: &Value{Code: 0x80, Number: 0x71}, Children: []Value{{Code: 0xc1, Children: []Value{{Code: 0xa3, Data: []byte("rejected")}, value}}}}, MaxFrameBytes)
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, 0, 0x53, 0x75, 0xa0, 1, 'x')
		_, err = DecodeMessage(wire)
		valid := value.Code == 0 && len(value.Children[0].Children) != 0
		if valid != (err == nil) {
			t.Fatalf("value=%+v err=%v", value, err)
		}
	}
}
