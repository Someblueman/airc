package xmppwire

import (
	"errors"
	"io"
	"strings"
	"testing"
)

const opening = `<s:stream xmlns:s="http://etherx.jabber.org/streams" xmlns="jabber:client" from="example" version="1.0">`

func TestStreamInheritedNamespacesAndJoinedClose(t *testing.T) {
	s := NewStream(strings.NewReader(`<?xml version='1.0' encoding='UTF-8'?>` + opening + `<message><body>whole 世界  </body></message><presence/></s:stream>`))
	if e, err := s.Open(); err != nil || e.Name != streamName {
		t.Fatalf("open: %+v %v", e, err)
	}
	e, err := s.Read()
	if err != nil || e.Name.Space != "jabber:client" {
		t.Fatalf("child: %+v %v", e, err)
	}
	body, err := e.Content[0].Element.Text()
	if err != nil || body != "whole 世界  " {
		t.Fatalf("body: %q %v", body, err)
	}
	if e, err := s.Read(); err != nil || e.Name.Local != "presence" {
		t.Fatalf("second: %+v %v", e, err)
	}
	if _, err := s.Read(); err != io.EOF {
		t.Fatal("explicit close:", err)
	}
	if _, err := s.Read(); !errors.Is(err, ErrElement) {
		t.Fatal("closed stream usable:", err)
	}
}

func TestStreamRestartPreservesBufferedBytesAndDropsNamespaces(t *testing.T) {
	s := NewStream(strings.NewReader(opening + `<success xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>` +
		`<s:stream xmlns:s="http://etherx.jabber.org/streams" xmlns="jabber:client"><s:features/></s:stream>`))
	if _, err := s.Open(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(); err != nil {
		t.Fatal(err)
	}
	if err := s.Restart(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(); err != nil {
		t.Fatal(err)
	}
	if e, err := s.Read(); err != nil || e.Name.Space != streamName.Space {
		t.Fatalf("restart: %+v %v", e, err)
	}
	bad := NewStream(strings.NewReader(opening + `<success xmlns="urn:test"/><s:stream xmlns="jabber:client">`))
	_, _ = bad.Open()
	_, _ = bad.Read()
	_ = bad.Restart()
	if _, err := bad.Open(); !errors.Is(err, ErrElement) {
		t.Fatal("old prefix survived restart:", err)
	}
}

func TestStreamRefusalsAreTerminalAndBoundInput(t *testing.T) {
	for _, child := range []string{
		`<message>`, `<message><!--hidden--></message>`, `<p:message/>`,
		`<message xmlns:p="http://www.w3.org/2000/xmlns/"/>`,
		`<message><body>` + strings.Repeat("x", MaxElement+1),
		strings.Repeat(" ", MaxElement+1),
		strings.Repeat("<message>", MaxDepth+1),
	} {
		s := NewStream(strings.NewReader(opening + child))
		if _, err := s.Open(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Read(); !errors.Is(err, ErrElement) {
			t.Fatalf("accepted %q: %v", child[:min(len(child), 40)], err)
		}
		if err := s.Restart(); !errors.Is(err, ErrElement) {
			t.Fatal("refusal restart allowed")
		}
	}
	for _, raw := range []string{"", `<message xmlns="jabber:client"/>`, opening[:20], strings.Repeat(" ", MaxElement+1)} {
		if _, err := NewStream(strings.NewReader(raw)).Open(); !errors.Is(err, ErrElement) {
			t.Fatal("invalid root accepted:", err)
		}
	}
	for _, pi := range []string{`<?xml version="1.1"?>`, `<?xml version="1.0" standalone="yes"?>`, `<?xml version="1.0" encoding="UTF-16"?>`, `<?other data?>`} {
		if _, err := NewStream(strings.NewReader(pi + opening)).Open(); !errors.Is(err, ErrElement) {
			t.Fatal("invalid declaration accepted:", err)
		}
	}
	if _, err := NewStream(nil).Open(); !errors.Is(err, ErrElement) {
		t.Fatal("nil input accepted")
	}
}
