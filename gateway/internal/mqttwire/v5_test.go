package mqttwire

import (
	"bytes"
	"testing"
)

func TestReadV5PublishRoundTrip(t *testing.T) {
	props := Properties{ResponseTopic: "replies/a", CorrelationData: []byte("c1"), User: [][2]string{{"k", "v"}}}
	p, err := ReadV5(bytes.NewReader(PublishV5("airc/requests", 7, 1, false, props, []byte("{}"))))
	if err != nil || p.Kind != Publish || p.Topic != "airc/requests" || p.ID != 7 || p.QoS != 1 || p.Payload != "{}" {
		t.Fatalf("%+v %v", p, err)
	}
	if p.Props.ResponseTopic != "replies/a" || string(p.Props.CorrelationData) != "c1" || !hasUser(p.Props, "k", "v") {
		t.Fatalf("properties %+v", p.Props)
	}
}

func FuzzReadV5(f *testing.F) {
	f.Add(PublishV5("airc/requests", 1, 1, false, Properties{ResponseTopic: "r", CorrelationData: []byte("c")}, []byte("{}")))
	f.Add(PublishV5("t", 0, 0, false, Properties{}, []byte("x")))
	f.Add([]byte{0x82, 9, 0, 1, 0, 0, 4, 'a', '/', 'b', 'c', 1})
	f.Add([]byte{0xa2, 6, 0, 2, 0, 0, 1, 'x'})
	f.Add([]byte{0x40, 2, 0, 1})
	f.Add([]byte{0xe0, 0})
	f.Add([]byte{0xc0, 0})
	f.Fuzz(func(t *testing.T, wire []byte) {
		p, err := ReadV5(bytes.NewReader(wire))
		if err != nil {
			return
		}
		switch p.Kind {
		case Publish:
			if p.QoS > 2 || p.QoS > 0 && p.ID == 0 || len(p.Payload) > MaxBodyBytesV5 {
				t.Fatalf("invalid decoded publication %+v", p)
			}
		case Subscribe:
			if p.ID == 0 || len(p.Subscriptions) == 0 {
				t.Fatalf("invalid decoded subscription %+v", p)
			}
		case Unsubscribe:
			if p.ID == 0 || len(p.Filters) == 0 {
				t.Fatalf("invalid decoded unsubscription %+v", p)
			}
		}
	})
}

func hasUser(props Properties, key, value string) bool {
	got, ok := props.UserProperty(key)
	return ok && got == value
}

// A property identifier in more than one byte is refused, not read as the
// property its low bits name.
func TestReadV5RefusesLongPropertyID(t *testing.T) {
	wire := []byte{0x30, 13, 0, 1, 't', 8, 0xa6, 0x02, 0, 1, 'k', 0, 1, 'v', 'x'}
	if p, err := ReadV5(bytes.NewReader(wire)); err == nil {
		t.Fatalf("read %+v", p)
	}
	wire[6], wire[7] = 0x26, 0 // the same property, encoded in one byte
	wire = append(wire[:7], wire[8:]...)
	wire[1], wire[5] = 12, 7
	if p, err := ReadV5(bytes.NewReader(wire)); err != nil || !hasUser(p.Props, "k", "v") {
		t.Fatalf("%+v %v", p, err)
	}
}
