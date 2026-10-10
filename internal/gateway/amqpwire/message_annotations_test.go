package amqpwire

import "testing"

func TestMessageRetainsUnsupportedAnnotationPolicy(t *testing.T) {
	for _, code := range []byte{0x71, 0x72, 0x78} {
		for _, tc := range []struct {
			key         Value
			unsupported bool
		}{
			{Value{Code: 0xa3, Data: []byte("x-opt-unknown")}, false},
			{Value{Code: 0xa3, Data: []byte("x-required")}, true},
			{Value{Code: 0x80, Number: 42}, true},
		} {
			section, err := EncodeValue(Value{Descriptor: &Value{Code: 0x80, Number: uint64(code)}, Children: []Value{{Code: 0xc1, Children: []Value{tc.key, {Code: 0x70, Number: 1}}}}}, MaxFrameBytes)
			if err != nil {
				t.Fatal(err)
			}
			data := []byte{0, 0x53, 0x75, 0xa0, 1, 'x'}
			wire := append(section, data...)
			if code == 0x78 {
				wire = append(data, section...)
			}
			message, err := DecodeMessage(wire)
			if err != nil || message.UnsupportedAnnotations != tc.unsupported || string(message.Body) != "x" {
				t.Fatal(code, message, err)
			}
		}
	}
}
