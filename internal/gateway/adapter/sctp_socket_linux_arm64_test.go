//go:build linux && arm64

package adapter

import (
	"errors"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestSCTPOrderedRecordsIPv4IPv6(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(address, func(t *testing.T) {
			listener, err := ListenSCTP(address, SCTPOrdered)
			if err != nil {
				t.Fatalf("ListenSCTP(%q): %v", address, err)
			}
			defer listener.Close()
			if listener.Addr().Network() != "sctp" {
				t.Fatalf("listener network = %q", listener.Addr().Network())
			}
			client := sctpTestClient(t, listener.Addr())
			defer client.close()
			accepted := acceptSCTPTestConn(t, listener)
			defer accepted.Close()

			client.send(t, []byte("first-record"))
			client.send(t, []byte("second-record"))
			for _, want := range []string{"first-record", "second-record"} {
				buffer := make([]byte, sctpRecordLimit)
				n, err := accepted.Read(buffer)
				if err != nil || string(buffer[:n]) != want {
					t.Fatalf("whole-record read = %q, %v; want %q", buffer[:n], err, want)
				}
			}

			if n, err := accepted.Write([]byte("reply-record")); err != nil || n != len("reply-record") {
				t.Fatalf("record write = %d, %v", n, err)
			}
			record := client.recv(t, 64, SCTPOrdered)
			if string(record) != "reply-record" {
				t.Fatalf("reply = %q", record)
			}

			if err := accepted.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if _, err := accepted.Read(make([]byte, 16)); !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("read deadline error = %v", err)
			}
			if err := accepted.SetReadDeadline(time.Time{}); err != nil {
				t.Fatal(err)
			}
			blocked := make(chan error, 1)
			go func() { _, err := accepted.Read(make([]byte, 16)); blocked <- err }()
			time.Sleep(20 * time.Millisecond)
			if err := accepted.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-blocked:
				if err == nil {
					t.Fatal("blocked Read succeeded after connection close")
				}
			case <-time.After(time.Second):
				t.Fatal("connection close did not wake Read")
			}
		})
	}
}

func TestSCTPUnorderedRecordsIPv4IPv6(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(address, func(t *testing.T) {
			listener, err := ListenSCTP(address, SCTPUnordered)
			if err != nil {
				t.Fatalf("ListenSCTP(%q, unordered): %v", address, err)
			}
			defer listener.Close()
			client := sctpTestClient(t, listener.Addr())
			defer client.close()
			accepted := acceptSCTPTestConn(t, listener)
			defer accepted.Close()

			client.sendInfo(t, []byte("unordered-request"), sctpMessageUnordered, false)
			buffer := make([]byte, sctpRecordLimit)
			n, err := accepted.Read(buffer)
			if err != nil || string(buffer[:n]) != "unordered-request" {
				t.Fatalf("unordered request read = %q, %v", buffer[:n], err)
			}
			if n, err := accepted.Write([]byte("unordered-reply")); err != nil || n != len("unordered-reply") {
				t.Fatalf("unordered reply write = %d, %v", n, err)
			}
			if got := client.recv(t, 64, SCTPUnordered); string(got) != "unordered-reply" {
				t.Fatalf("unordered reply = %q", got)
			}
		})
	}
}

func TestSCTPRejectsInvalidDeliveryMode(t *testing.T) {
	if _, err := ListenSCTP("127.0.0.1:0", SCTPMode(2)); err == nil {
		t.Fatal("invalid SCTP delivery mode was accepted")
	}
}

func TestSCTPRejectsOversizedRecordAndClosesConnection(t *testing.T) {
	for _, mode := range []SCTPMode{SCTPOrdered, SCTPUnordered} {
		t.Run(strconv.Itoa(int(mode)), func(t *testing.T) {
			listener, err := ListenSCTP("127.0.0.1:0", mode)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			client := sctpTestClient(t, listener.Addr())
			defer client.close()
			accepted := acceptSCTPTestConn(t, listener)
			defer accepted.Close()
			client.sendMode(t, make([]byte, sctpRecordLimit+1), mode)
			if _, err := accepted.Read(make([]byte, sctpRecordLimit+1)); !errors.Is(err, errSCTPRecordTooLarge) {
				t.Fatalf("oversized receive error = %v", err)
			}
			if _, err := accepted.Read(make([]byte, 1)); err == nil {
				t.Fatal("read after oversized record did not fail")
			}
		})
	}
}

func TestSCTPRejectsRecordLargerThanCallerBuffer(t *testing.T) {
	listener, err := ListenSCTP("127.0.0.1:0", SCTPOrdered)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client := sctpTestClient(t, listener.Addr())
	defer client.close()
	accepted := acceptSCTPTestConn(t, listener)
	defer accepted.Close()
	client.send(t, []byte("record-does-not-fit"))
	if _, err := accepted.Read(make([]byte, 4)); !errors.Is(err, errSCTPReadBufferTooSmall) {
		t.Fatalf("short read buffer error = %v", err)
	}
	if _, err := accepted.Read(make([]byte, sctpRecordLimit)); err == nil {
		t.Fatal("read after short-buffer refusal succeeded")
	}
}

func TestSCTPWriteSupportsResponseTokenLimit(t *testing.T) {
	for _, mode := range []SCTPMode{SCTPOrdered, SCTPUnordered} {
		t.Run(strconv.Itoa(int(mode)), func(t *testing.T) {
			listener, err := ListenSCTP("127.0.0.1:0", mode)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			client := sctpTestClient(t, listener.Addr())
			defer client.close()
			accepted := acceptSCTPTestConn(t, listener)
			defer accepted.Close()
			want := make([]byte, 20<<10)
			if n, err := accepted.Write(want); err != nil || n != len(want) {
				t.Fatalf("large response write = %d, %v", n, err)
			}
			got := client.recv(t, len(want), mode)
			if len(got) != len(want) {
				t.Fatalf("large response length = %d, want %d", len(got), len(want))
			}
			if n, err := accepted.Write(make([]byte, sctpWriteRecordLimit+1)); n != 0 || !errors.Is(err, errSCTPRecordTooLarge) {
				t.Fatalf("oversized response write = %d, %v", n, err)
			}
		})
	}
}

func TestSCTPListenerCloseWakesAccept(t *testing.T) {
	listener, err := ListenSCTP("127.0.0.1:0", SCTPOrdered)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := listener.Accept(); done <- err }()
	time.Sleep(20 * time.Millisecond)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept after Close = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener close did not wake Accept")
	}
}
