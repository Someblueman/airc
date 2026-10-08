package xmppwire

import (
	"encoding/xml"
	"errors"
	"strings"
	"testing"
)

func TestWholeNamespaceQualifiedBody(t *testing.T) {
	e, err := Parse([]byte(`<c:message xmlns:c="jabber:client" from="room@example/alice"><c:body>whole 世界 &amp; <![CDATA[<body>]]>  </c:body><id:stanza-id xmlns:id="urn:xmpp:sid:0" id="opaque" by="room@example"/></c:message>`))
	if err != nil || e.Name != (xml.Name{Space: "jabber:client", Local: "message"}) || len(e.Content) != 2 {
		t.Fatalf("parse: %+v %v", e, err)
	}
	body, err := e.Content[0].Element.Text()
	if err != nil || body != "whole 世界 & <body>  " {
		t.Fatalf("whole body: %q %v", body, err)
	}
	if e.Content[1].Element.Name.Space != "urn:xmpp:sid:0" || e.Attributes[0].Value != "room@example/alice" {
		t.Fatal("namespace or attribute changed")
	}
	if _, err := e.Text(); !errors.Is(err, ErrElement) {
		t.Fatal("nested markup accepted as plain text")
	}
}

func TestElementRefusals(t *testing.T) {
	for _, raw := range []string{
		"", `<message/>`, `<p:message/>`, `<message xmlns="jabber:client">`,
		`<message xmlns="jabber:client" a="1" a="2"/>`,
		`<message xmlns="jabber:client" xmlns:a="urn:test" xmlns:b="urn:test" a:x="1" b:x="2"/>`,
		`<message xmlns="jabber:client" p:fake="x"/>`,
		`<message xmlns="jabber:client" xmlns:xml="urn:fake"/>`,
		`<message xmlns="jabber:client" xmlns:xmlns="urn:fake"/>`,
		`<message xmlns="jabber:client" xmlns:p="relative"/>`,
		`<!DOCTYPE message [<!ENTITY x "expanded">]><message xmlns="jabber:client">&x;</message>`,
		`<?xml version="1.0"?><message xmlns="jabber:client"/>`,
		`<message xmlns="jabber:client"><!--hidden--></message>`,
		`<message xmlns="jabber:client">&unknown;</message>`,
		`<message xmlns="jabber:client"/><message xmlns="jabber:client"/>`,
		`<message xmlns="jabber:client">` + string([]byte{0xff}) + `</message>`,
		`<message xmlns="jabber:client">&#0;</message>`,
		"\u00a0<message xmlns=\"jabber:client\"/>",
	} {
		if _, err := Parse([]byte(raw)); !errors.Is(err, ErrElement) {
			t.Errorf("accepted %q: %v", raw, err)
		}
	}
}

func TestElementResourceBounds(t *testing.T) {
	prefix, suffix := `<body xmlns="jabber:client">`, `</body>`
	whole := prefix + strings.Repeat("x", MaxElement-len(prefix)-len(suffix)) + suffix
	if _, err := Parse([]byte(whole)); err != nil {
		t.Fatal("exact byte bound:", err)
	}
	for _, raw := range []string{
		whole + " ",
		`<a xmlns="jabber:client">` + strings.Repeat("<a>", MaxDepth) + strings.Repeat("</a>", MaxDepth) + `</a>`,
		`<a xmlns="jabber:client">` + strings.Repeat("<b/>", MaxTokens) + `</a>`,
		`<a xmlns="jabber:client"` + attributes(MaxAttributes) + `/>`,
	} {
		if _, err := Parse([]byte(raw)); !errors.Is(err, ErrElement) {
			t.Fatal("resource bound not enforced:", err)
		}
	}
}

func attributes(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(" a" + strings.Repeat("x", i+1) + `="v"`)
	}
	return b.String()
}
