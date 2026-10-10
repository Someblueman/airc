package xmppwire

import (
	"encoding/xml"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeWholeElementRoundTrip(t *testing.T) {
	e := &Element{Name: xml.Name{Space: "jabber:client", Local: "message"},
		Attributes: []xml.Attr{{Name: xml.Name{Local: "to"}, Value: "room@example"}},
		Content: []Content{{Element: &Element{Name: xml.Name{Space: "jabber:client", Local: "body"},
			Content: []Content{{Text: "whole 世界 <&>\r\n  "}}}}}}
	raw, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(raw)
	if err != nil || !reflect.DeepEqual(got, e) {
		t.Fatalf("round trip changed content: %s %+v %v", raw, got, err)
	}
}

func TestEncodeRefusesIncompleteOrOversizedOutput(t *testing.T) {
	cycle := &Element{Name: xml.Name{Space: "jabber:client", Local: "message"}}
	cycle.Content = []Content{{Element: cycle}}
	for _, e := range []*Element{
		nil, {}, cycle,
		{Name: xml.Name{Space: "jabber:client", Local: "body"}, Content: []Content{{Text: strings.Repeat("&", MaxElement)}}},
		{Name: xml.Name{Space: "jabber:client", Local: "body"}, Content: []Content{{Text: "\x00"}}},
		{Name: xml.Name{Space: "jabber:client", Local: "body"}, Content: []Content{{Text: string([]byte{0xff})}}},
		{Name: xml.Name{Space: "jabber:client", Local: strings.Repeat("a", MaxElement+1)}},
		{Name: xml.Name{Space: "jabber:client", Local: "message"}, Content: []Content{{Text: "hidden", Element: cycle}}},
	} {
		if raw, err := Encode(e); raw != nil || !errors.Is(err, ErrElement) {
			t.Fatalf("returned partial/invalid output: %d %v", len(raw), err)
		}
	}
}
