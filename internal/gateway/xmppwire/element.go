// Package xmppwire provides bounded XML elements for the XMPP bridge. Elements
// carry data only; parsing does not authenticate their sender or provenance.
package xmppwire

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxElement = 64 << 10
const MaxDepth = 16
const MaxTokens = 2048
const MaxAttributes = 32

var ErrElement = errors.New("invalid or oversized XMPP XML element")

// Element preserves mixed content in order, including significant whitespace.
// Names use expanded namespaces; lexical prefixes are not identity.
type Element struct {
	Name       xml.Name
	Attributes []xml.Attr
	Content    []Content
}

type Content struct {
	Text    string
	Element *Element
}

// Parse accepts one standalone, namespace-qualified element. A stream owner
// must supply inherited namespace declarations when framing stream children.
// DTDs, processing instructions and comments are outside this strict profile.
func Parse(raw []byte) (*Element, error) {
	if len(raw) == 0 || len(raw) > MaxElement || !utf8.Valid(raw) {
		return nil, ErrElement
	}
	d := xml.NewDecoder(bytes.NewReader(raw))
	var root *Element
	var stack []*Element
	for count := 0; ; count++ {
		t, err := d.Token()
		if err == io.EOF {
			if root != nil && len(stack) == 0 {
				return root, nil
			}
			return nil, ErrElement
		}
		if err != nil || count >= MaxTokens {
			return nil, ErrElement
		}
		switch t := t.(type) {
		case xml.StartElement:
			if len(stack) >= MaxDepth || len(t.Attr) > MaxAttributes || !qualified(t.Name) {
				return nil, ErrElement
			}
			e := &Element{Name: t.Name}
			seen := map[xml.Name]bool{}
			for _, a := range t.Attr {
				if seen[a.Name] || (a.Name.Space != "" && a.Name.Space != "xmlns" && !qualified(a.Name)) {
					return nil, ErrElement
				}
				seen[a.Name] = true
				if a.Name.Space == "xmlns" || a.Name == (xml.Name{Local: "xmlns"}) {
					if !strings.Contains(a.Value, ":") || a.Name == (xml.Name{Space: "xmlns", Local: "xmlns"}) ||
						(a.Name == (xml.Name{Space: "xmlns", Local: "xml"}) && a.Value != "http://www.w3.org/XML/1998/namespace") ||
						a.Value == "http://www.w3.org/2000/xmlns/" ||
						(a.Value == "http://www.w3.org/XML/1998/namespace" && a.Name != (xml.Name{Space: "xmlns", Local: "xml"})) {
						return nil, ErrElement
					}
					continue
				}
				e.Attributes = append(e.Attributes, a)
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, ErrElement
				}
				root = e
			} else {
				p := stack[len(stack)-1]
				p.Content = append(p.Content, Content{Element: e})
			}
			stack = append(stack, e)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].Name != t.Name {
				return nil, ErrElement
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.Trim(string(t), " \t\r\n") != "" {
					return nil, ErrElement
				}
			} else {
				p := stack[len(stack)-1]
				p.Content = append(p.Content, Content{Text: string(t)})
			}
		default:
			return nil, ErrElement
		}
	}
}

func qualified(n xml.Name) bool {
	return n.Local != "" && len(n.Local) <= MaxElement && len(n.Space) <= MaxElement &&
		utf8.ValidString(n.Local) && utf8.ValidString(n.Space) && strings.Contains(n.Space, ":")
}

// Text returns a whole simple text element, refusing nested markup.
func (e *Element) Text() (string, error) {
	if e == nil {
		return "", ErrElement
	}
	var b strings.Builder
	for _, c := range e.Content {
		if c.Element != nil {
			return "", ErrElement
		}
		b.WriteString(c.Text)
	}
	return b.String(), nil
}
