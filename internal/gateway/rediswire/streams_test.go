package rediswire

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func streamReply(id, fields string) string {
	return "*1\r\n*2\r\n$6\r\nevents\r\n*1\r\n*2\r\n" + fmt.Sprintf("$%d\r\n%s\r\n", len(id), id) + fields
}

func TestStreamEntryRead(t *testing.T) {
	for _, payload := range [][]byte{[]byte("signed.intent"), {0, '\r', '\n', 255}, bytes.Repeat([]byte("x"), MaxBulk)} {
		fields := fmt.Sprintf("*2\r\n$6\r\nintent\r\n$%d\r\n%s\r\n", len(payload), payload)
		wire := streamReply("123-4", fields)
		r := bufio.NewReader(strings.NewReader(wire + ":1\r\n"))
		e, err := ReadStreamEntry(r, "events")
		if err != nil || e == nil || e.ID != "123-4" || e.Deleted || !bytes.Equal(e.Payload, payload) {
			t.Fatalf("entry = %v, error = %v", e, err)
		}
		v, err := Read(r)
		if err != nil || v.Type != Integer || v.Integer != 1 {
			t.Fatalf("pipeline = %v, %v", v, err)
		}
		if _, err := Read(bufio.NewReader(strings.NewReader(wire))); err == nil {
			t.Fatal("ordinary reader accepted five array levels")
		}
	}
}

func TestStreamEntryAbsentAndDeleted(t *testing.T) {
	for _, wire := range []string{"*-1\r\n", "*1\r\n*2\r\n$6\r\nevents\r\n*0\r\n"} {
		e, err := ReadStreamEntry(bufio.NewReader(strings.NewReader(wire)), "events")
		if err != nil || e != nil {
			t.Fatalf("absent = %v, %v", e, err)
		}
	}
	e, err := ReadStreamEntry(bufio.NewReader(strings.NewReader(streamReply("1-0", "*-1\r\n"))), "events")
	if err != nil || e == nil || !e.Deleted || e.ID != "1-0" || e.Payload != nil {
		t.Fatalf("deleted = %v, %v", e, err)
	}
}

func TestStreamEntryRefusals(t *testing.T) {
	fields := "*2\r\n$6\r\nintent\r\n$1\r\nx\r\n"
	for _, wire := range []string{
		"$-1\r\n", "*0\r\n", "*2\r\n*-1\r\n*-1\r\n",
		strings.Replace(streamReply("1-0", fields), "events", "others", 1),
		streamReply("01-0", fields), streamReply("0-0", fields),
		streamReply("1-0", "$-1\r\n"), streamReply("1-0", "*0\r\n"),
		streamReply("1-0", strings.Replace(fields, "intent", "author", 1)),
		streamReply("1-0", "*2\r\n$6\r\nintent\r\n$-1\r\n"),
		streamReply("1-0", "*2\r\n$6\r\nintent\r\n$0\r\n\r\n"),
		streamReply("1-0", "*4\r\n$6\r\nintent\r\n$1\r\nx\r\n$6\r\nintent\r\n$1\r\ny\r\n"),
		streamReply("1-0", "*2\r\n$6\r\nintent\r\n$32769\r\n"),
		streamReply("1-0", "*2\r\n$6\r\nintent\r\n*1\r\n*0\r\n"),
		strings.Replace(streamReply("1-0", fields), "*1\r\n*2\r\n$3", "*2\r\n*2\r\n$3", 1),
	} {
		if e, err := ReadStreamEntry(bufio.NewReader(strings.NewReader(wire)), "events"); err == nil {
			t.Fatalf("accepted %q: %v", wire, e)
		}
	}
	if _, err := ReadStreamEntry(nil, "events"); err == nil {
		t.Fatal("nil reader accepted")
	}
}

func TestStreamEntryID(t *testing.T) {
	for _, id := range []string{"1-0", "0-1", "18446744073709551615-18446744073709551615"} {
		if !StreamEntryID(id) {
			t.Fatalf("refused %q", id)
		}
	}
	for _, id := range []string{"", "0-0", "0", ">", "$", "1-*", "01-0", "1-00", "+1-0", "1--1", "1-2-3", "18446744073709551616-0"} {
		if StreamEntryID(id) {
			t.Fatalf("accepted %q", id)
		}
	}
}
