package syslogwire

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

var ErrSignedBlock = errors.New("invalid or unsupported signed syslog block")

// SignedBlock owns exact wire bytes. Parsing alone establishes no authority,
// freshness, or permission to post any of the referenced records.
type SignedBlock struct {
	Record                      Record
	Certificate                 bool
	Version                     string
	Reboot, Counter, First      uint64
	Group, Priority             int
	Hashes                      [][]byte
	Total, Index                int
	Fragment, Signature, Signed []byte
}

// ParseSignedBlock accepts the RFC 5848 SHA256/OpenPGP-DSA profile (0121).
// It removes only the original SIGN parameter, without reserializing headers,
// other structured data, or MSG. Certificate payloads are bounded to 32 KiB.
func ParseSignedBlock(raw []byte) (SignedBlock, error) {
	r, err := Parse(raw)
	if err != nil {
		return SignedBlock{}, err
	}
	sd := r.StructuredData
	start, end := -1, -1
	cert := false
	for p := 0; p < len(sd) && sd[p] == '['; {
		a := p
		p++
		nameStart := p
		for p < len(sd) && sd[p] != ' ' && sd[p] != ']' {
			p++
		}
		name := string(sd[nameStart:p])
		quoted := false
		for p < len(sd) {
			if quoted && sd[p] == '\\' && p+1 < len(sd) {
				p += 2
				continue
			}
			if sd[p] == '"' {
				quoted = !quoted
			}
			if !quoted && sd[p] == ']' {
				p++
				break
			}
			p++
		}
		if name == "ssign" || name == "ssign-cert" {
			if start >= 0 {
				return SignedBlock{}, ErrSignedBlock
			}
			start, end, cert = a, p, name == "ssign-cert"
		}
	}
	if start < 0 {
		return SignedBlock{}, ErrSignedBlock
	}
	names := []string{"VER", "RSID", "SG", "SPRI", "GBC", "FMN", "CNT", "HB", "SIGN"}
	id := "ssign"
	if cert {
		id = "ssign-cert"
		names = []string{"VER", "RSID", "SG", "SPRI", "TPBL", "INDEX", "FLEN", "FRAG", "SIGN"}
	}
	p := start + 1 + len(id)
	values := make([]string, len(names))
	signStart := 0
	for i, name := range names {
		prefix := " " + name + "=\""
		if !bytes.HasPrefix(sd[p:end], []byte(prefix)) {
			return SignedBlock{}, ErrSignedBlock
		}
		if name == "SIGN" {
			signStart = p
		}
		p += len(prefix)
		a := p
		for p < end && sd[p] != '"' {
			if sd[p] < 32 || sd[p] > 126 || sd[p] == '\\' || sd[p] == ']' {
				return SignedBlock{}, ErrSignedBlock
			}
			p++
		}
		if p >= end {
			return SignedBlock{}, ErrSignedBlock
		}
		values[i] = string(sd[a:p])
		p++
	}
	if p != end-1 || sd[p] != ']' || values[0] != "0121" {
		return SignedBlock{}, ErrSignedBlock
	}
	b := SignedBlock{Record: r, Certificate: cert, Version: values[0]}
	var ok bool
	if b.Reboot, ok = blockNumber(values[1], 9999999999); !ok {
		return SignedBlock{}, ErrSignedBlock
	}
	g, ok := blockNumber(values[2], 3)
	if !ok {
		return SignedBlock{}, ErrSignedBlock
	}
	b.Group = int(g)
	pri, ok := blockNumber(values[3], 191)
	if !ok {
		return SignedBlock{}, ErrSignedBlock
	}
	b.Priority = int(pri)
	if b.Group == 0 && b.Priority != 0 {
		return SignedBlock{}, ErrSignedBlock
	}
	if cert {
		total, ok := blockNumber(values[4], MaxRecord)
		if !ok || total == 0 {
			return SignedBlock{}, ErrSignedBlock
		}
		index, ok := blockNumber(values[5], total)
		if !ok || index == 0 {
			return SignedBlock{}, ErrSignedBlock
		}
		length, ok := blockNumber(values[6], 9999)
		if !ok || length == 0 || index-1+length > total || uint64(len(values[7])) != length {
			return SignedBlock{}, ErrSignedBlock
		}
		b.Total, b.Index, b.Fragment = int(total), int(index), []byte(values[7])
	} else {
		if b.Counter, ok = blockNumber(values[4], 9999999999); !ok {
			return SignedBlock{}, ErrSignedBlock
		}
		if b.First, ok = blockNumber(values[5], 9999999999); !ok || b.First == 0 {
			return SignedBlock{}, ErrSignedBlock
		}
		count, ok := blockNumber(values[6], 99)
		if !ok || count == 0 {
			return SignedBlock{}, ErrSignedBlock
		}
		hashes := strings.Split(values[7], " ")
		if len(hashes) != int(count) {
			return SignedBlock{}, ErrSignedBlock
		}
		for _, text := range hashes {
			h, err := strictBase64(text)
			if err != nil || len(h) != 32 {
				return SignedBlock{}, ErrSignedBlock
			}
			b.Hashes = append(b.Hashes, h)
		}
	}
	b.Signature, err = strictBase64(values[8])
	if err != nil || len(b.Signature) < 6 || len(b.Signature) > 68 {
		return SignedBlock{}, ErrSignedBlock
	}
	// StructuredData is an owned slice into Raw; locate its original offset.
	offset := len(r.Raw) - len(r.StructuredData)
	if r.Message != nil {
		offset -= len(r.Message) + 1
	}
	b.Signed = append(bytes.Clone(r.Raw[:offset+signStart]), r.Raw[offset+p:]...)
	return b, nil
}

func blockNumber(s string, max uint64) (uint64, bool) {
	if len(s) == 0 || len(s) > 10 || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil && n <= max
}

func strictBase64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || base64.StdEncoding.EncodeToString(b) != s {
		return nil, ErrSignedBlock
	}
	return b, nil
}
