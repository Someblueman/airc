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
