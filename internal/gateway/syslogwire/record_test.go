package syslogwire

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
)

func TestRecordPreservesBoundariesAndBytes(t *testing.T) {
	records := []string{
		`<34>1 2003-10-11T22:14:15.003Z host app 123 ID47 [example@32473 a="日本語" a="again" escaped="a\]b\"c\\d\q"] ` + "\xef\xbb\xbfhello\nworld",
		"<0>1 - - - - - -",
		"<191>1 2026-10-07T01:02:03.123456-23:59 host app proc id - \x00\xffbinary",
	}
	var wire strings.Builder
	for _, raw := range records {
		wire.WriteString(strconv.Itoa(len(raw)) + " " + raw)
	}
	r := bufio.NewReaderSize(iotest.OneByteReader(strings.NewReader(wire.String())), 16)
	for _, want := range records {
		got, err := Read(r)
		if err != nil || string(got.Raw) != want {
			t.Fatalf("record changed: %q %v", got.Raw, err)
		}
		if got.Hostname == "" {
			t.Fatal("missing header")
		}
	}
	if _, err := Read(r); err != io.EOF {
		t.Fatal("missing stream boundary", err)
	}
	input := []byte(records[0])
	got, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	if got.Raw[0] != '<' {
		t.Fatal("borrowed input")
	}
}

func TestRecordRejectsMalformedSyntax(t *testing.T) {
	base := "<34>1 - host app proc id "
	cases := []string{
		"", "<034>1 - - - - - -", "<192>1 - - - - - -", "<-1>1 - - - - - -", "<34>2 - - - - - -", "<34>1  - - - - - -",
		base, base + "x", base + "-x", base + "[]", base + "[id][id]", base + "[id p=\"x]\"]", base + "[id p=\"x\" q]", base + "[id p=\"unterminated]",
		base + "[id p=\"\xff\"]", base + "- \xef\xbb\xbf\xff", base + "[" + strings.Repeat("a", 33) + "]",
		"<34>1 2026-02-30T00:00:00Z h a p i -", "<34>1 2026-01-01T00:00:60Z h a p i -", "<34>1 2026-01-01T00:00:00.1234567Z h a p i -",
		"<34>1 2026-01-01t00:00:00z h a p i -", "<34>1 2026-01-01T00:00:00+24:00 h a p i -", "<34>1 2026-01-01T00:00:00+01:60 h a p i -",
	}
	for i, raw := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			if _, err := Parse([]byte(raw)); !errors.Is(err, ErrRecord) {
				t.Fatalf("accepted %q: %v", raw, err)
			}
		})
	}
	for i, limit := range []int{255, 48, 128, 32} {
		fields := []string{"host", "app", "proc", "id"}
		fields[i] = strings.Repeat("x", limit+1)
		if _, err := Parse([]byte("<34>1 - " + strings.Join(fields, " ") + " -")); err == nil {
			t.Fatal("oversized header accepted")
		}
	}
}

func TestFramingBoundsAndTruncation(t *testing.T) {
	for _, wire := range []string{"0 ", "01 x", "-1 x", " x", "1x x", "32769 ", "999999999999999999999 "} {
		if _, err := Read(bufio.NewReader(strings.NewReader(wire))); !errors.Is(err, ErrRecord) {
			t.Fatalf("accepted length %q: %v", wire, err)
		}
	}
	for _, wire := range []string{"1", "12", "20 ", "20 <34>1 -"} {
		if _, err := Read(bufio.NewReader(strings.NewReader(wire))); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("truncation %q: %v", wire, err)
		}
	}
	prefix := "<34>1 - h a p i - "
	raw := []byte(prefix + strings.Repeat("x", MaxRecord-len(prefix)))
	got, err := Read(bufio.NewReader(bytes.NewReader(append([]byte(strconv.Itoa(len(raw))+" "), raw...))))
	if err != nil || !bytes.Equal(got.Raw, raw) {
		t.Fatal("maximum record rejected", err)
	}
	if _, err := Parse(append(raw, 'x')); !errors.Is(err, ErrRecord) {
		t.Fatal("oversized record accepted")
	}
}
