package rediswire

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRESP2DeclaredAndMalformedBounds(t *testing.T) {
	for _, wire := range []string{
		"$32769\r\n", "*129\r\n", "$18446744073709551616\r\n", "$-2\r\n", "*-2\r\n", "$+1\r\n", "$\r\n", "* 1\r\n", ":9223372036854775808\r\n", ":-9223372036854775809\r\n", ":1_0\r\n", ":\r\n", "+bad\n", "+bad\rX", "$1\r\naXX", ">%\r\n", "_\r\n", "AUTH password\r\n", "+" + strings.Repeat("a", MaxLine+1) + "\r\n",
	} {
		if _, err := Read(bufio.NewReader(strings.NewReader(wire))); !errors.Is(err, ErrFrame) {
			t.Fatalf("accepted %q: %v", wire, err)
		}
	}
	for _, header := range []string{"$32769\r\n", "*129\r\n"} {
		r := bufio.NewReader(strings.NewReader(header + "UNCONSUMED"))
		if _, err := Read(r); !errors.Is(err, ErrFrame) {
			t.Fatal(err)
		}
		if b, err := r.Peek(10); err != nil || string(b) != "UNCONSUMED" {
			t.Fatal("oversize body consumed", err)
		}
	}
	if _, err := Read(nil); !errors.Is(err, ErrFrame) {
		t.Fatal("nil reader")
	}
}

func TestRESP2ExactBoundsDepthAndFrameBudget(t *testing.T) {
	for _, v := range []Value{
		{Type: Simple, Bytes: bytes.Repeat([]byte("x"), MaxLine)},
		{Type: Bulk, Bytes: bytes.Repeat([]byte("x"), MaxBulk)},
		{Type: Array, Array: repeatValue(Value{Type: Integer}, MaxElements)},
	} {
		wire, err := Encode(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Read(bufio.NewReader(bytes.NewReader(wire))); err != nil {
			t.Fatal("exact bound", err)
		}
	}
	v := Value{Type: Integer}
	for range MaxDepth {
		v = Value{Type: Array, Array: []Value{v}}
	}
	wire, err := Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(bufio.NewReader(bytes.NewReader(wire))); err != nil {
		t.Fatal(err)
	}
	if _, err := Encode(Value{Type: Array, Array: []Value{v}}); !errors.Is(err, ErrFrame) {
		t.Fatal("depth encoded")
	}
	if _, err := Read(bufio.NewReader(bytes.NewReader(append([]byte("*1\r\n"), wire...)))); !errors.Is(err, ErrFrame) {
		t.Fatal("depth decoded")
	}
	v = Value{Type: Array, Array: []Value{{Type: Bulk, Bytes: bytes.Repeat([]byte("a"), MaxBulk)}, {Type: Bulk, Bytes: bytes.Repeat([]byte("b"), 32744)}}}
	wire, err = Encode(v)
	if err != nil || len(wire) != MaxFrame {
		t.Fatal("exact frame bound", len(wire), err)
	}
	if _, err := Read(bufio.NewReader(bytes.NewReader(wire))); err != nil {
		t.Fatal(err)
	}
	v.Array[1].Bytes = append(v.Array[1].Bytes, 'b')
	if b, err := Encode(v); !errors.Is(err, ErrFrame) || b != nil {
		t.Fatal("oversize frame encoded", err)
	}
	oversize := bytes.Replace(wire, []byte("$32744\r\n"), []byte("$32745\r\n"), 1)
	r := bufio.NewReader(bytes.NewReader(oversize))
	if _, err := Read(r); !errors.Is(err, ErrFrame) {
		t.Fatal("oversize frame decoded", err)
	}
	if b, err := r.Peek(1); err != nil || b[0] != 'b' {
		t.Fatal("budget refused after body consumption", err)
	}
}

func repeatValue(v Value, n int) []Value {
	values := make([]Value, n)
	for i := range values {
		values[i] = v
	}
	return values
}

func TestRESP2AggregateBudgetRefusesBeforeChildren(t *testing.T) {
	wire := "*3\r\n$32768\r\n" + strings.Repeat("a", 32768) + "\r\n$32700\r\n" + strings.Repeat("b", 32700) + "\r\n*128\r\nUNCONSUMED"
	r := bufio.NewReader(strings.NewReader(wire))
	if _, err := Read(r); !errors.Is(err, ErrFrame) {
		t.Fatal("aggregate minimum budget accepted", err)
	}
	if b, err := r.Peek(10); err != nil || string(b) != "UNCONSUMED" {
		t.Fatal("children consumed before budget refusal", err)
	}
}
