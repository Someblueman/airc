package xmppwire

import (
	"encoding/xml"
	"strings"
)

type elementBuilder struct {
	root  *Element
	stack []*Element
}

func (b *elementBuilder) add(t xml.Token) error {
	switch t := t.(type) {
	case xml.StartElement:
		if len(b.stack) >= MaxDepth || len(t.Attr) > MaxAttributes || !qualified(t.Name) {
			return ErrElement
		}
		e := &Element{Name: t.Name}
		seen := map[xml.Name]bool{}
		for _, a := range t.Attr {
			if seen[a.Name] || (a.Name.Space != "" && a.Name.Space != "xmlns" && !qualified(a.Name)) {
				return ErrElement
			}
			seen[a.Name] = true
			if a.Name.Space == "xmlns" || a.Name == (xml.Name{Local: "xmlns"}) {
				if !strings.Contains(a.Value, ":") || a.Name == (xml.Name{Space: "xmlns", Local: "xmlns"}) ||
					(a.Name == (xml.Name{Space: "xmlns", Local: "xml"}) && a.Value != "http://www.w3.org/XML/1998/namespace") ||
					a.Value == "http://www.w3.org/2000/xmlns/" ||
					(a.Value == "http://www.w3.org/XML/1998/namespace" && a.Name != (xml.Name{Space: "xmlns", Local: "xml"})) {
					return ErrElement
				}
				continue
			}
			e.Attributes = append(e.Attributes, a)
		}
		if len(b.stack) == 0 {
			if b.root != nil {
				return ErrElement
			}
			b.root = e
		} else {
			p := b.stack[len(b.stack)-1]
			p.Content = append(p.Content, Content{Element: e})
		}
		b.stack = append(b.stack, e)
	case xml.EndElement:
		if len(b.stack) == 0 || b.stack[len(b.stack)-1].Name != t.Name {
			return ErrElement
		}
		b.stack = b.stack[:len(b.stack)-1]
	case xml.CharData:
		if len(b.stack) == 0 {
			if strings.Trim(string(t), " \t\r\n") != "" {
				return ErrElement
			}
		} else {
			p := b.stack[len(b.stack)-1]
			p.Content = append(p.Content, Content{Text: string(t)})
		}
	default:
		return ErrElement
	}
	return nil
}
