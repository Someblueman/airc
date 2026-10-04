package adapter

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestFramedStreamCleanEOFAtBoundary(t *testing.T) {
	err := serveFramedStreamTransport(context.Background(), &readerStream{}, nil, make(chan struct{}, 1), false, false, true, []string{"long_poll", "framed_stream"})
	if err != nil {
		t.Fatalf("clean boundary EOF: %v", err)
	}
}

func TestFramedStreamCleanEOFDrainsAdmittedHandler(t *testing.T) {
	body, err := json.Marshal(FrameRequest{Version: 1, CorrelationID: "one", Profile: "signed", Envelope: "opaque"})
	if err != nil {
		t.Fatal(err)
	}
	framed := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(framed[:4], uint32(len(body)))
	copy(framed[4:], body)
	stream := &readerStream{Reader: *bytes.NewReader(framed)}
	err = serveFramedStreamTransport(context.Background(), stream, nil, make(chan struct{}, 1), false, false, true, []string{"long_poll", "framed_stream"})
	if err != nil {
		t.Fatalf("clean EOF after admitted request: %v", err)
	}
	output := stream.output.Bytes()
	if len(output) < 4 || int(binary.BigEndian.Uint32(output[:4])) != len(output)-4 {
		t.Fatalf("missing complete drained response: %q", output)
	}
	var response FrameResponse
	if err := json.Unmarshal(output[4:], &response); err != nil {
		t.Fatal(err)
	}
	if response.CorrelationID != "one" || response.Event != "error" || response.Error == nil {
		t.Fatalf("drained handler response: %+v", response)
	}
}

func TestFramedStreamPartialEOFIsReturned(t *testing.T) {
	err := serveFramedStreamTransport(context.Background(), &readerStream{Reader: *bytes.NewReader([]byte{0})}, nil, make(chan struct{}, 1), false, false, true, []string{"long_poll", "framed_stream"})
	if err == nil || errors.Is(err, errFramedCleanEOF) {
		t.Fatalf("partial frame error = %v, want non-clean EOF", err)
	}
}

func TestFramedStreamReadDeadlineErrorIsReturned(t *testing.T) {
	stream := deadlineErrorStream{}
	err := serveFramedStreamTransport(context.Background(), &stream, nil, make(chan struct{}, 1), false, false, true, []string{"long_poll", "framed_stream"})
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("deadline error = %v, want %v", err, os.ErrDeadlineExceeded)
	}
}

func TestFramedStreamCancellationClosesRead(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveFramedStreamTransport(ctx, server, nil, make(chan struct{}, 1), false, false, true, []string{"long_poll", "framed_stream"})
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close the stream")
	}
}

func TestFramedStreamReadDeadlineSetterErrorIsReturned(t *testing.T) {
	want := errors.New("read deadline unavailable")
	stream := deadlineSetterErrorStream{err: want}
	err := serveFramedStreamTransport(context.Background(), &stream, nil, make(chan struct{}, 1), false, false, true, []string{"long_poll", "framed_stream"})
	if !errors.Is(err, want) {
		t.Fatalf("deadline setter error = %v, want %v", err, want)
	}
	if stream.reads != 0 {
		t.Fatalf("read count = %d, want 0", stream.reads)
	}
}

type deadlineErrorStream struct{}

type readerStream struct {
	bytes.Reader
	output bytes.Buffer
}

type deadlineSetterErrorStream struct {
	err   error
	reads int
}

func (s *readerStream) Write(p []byte) (int, error)    { return s.output.Write(p) }
func (*readerStream) Close() error                     { return nil }
func (*readerStream) SetReadDeadline(time.Time) error  { return nil }
func (*readerStream) SetWriteDeadline(time.Time) error { return nil }

func (*deadlineErrorStream) Read([]byte) (int, error)         { return 0, os.ErrDeadlineExceeded }
func (*deadlineErrorStream) Write(p []byte) (int, error)      { return len(p), nil }
func (*deadlineErrorStream) Close() error                     { return nil }
func (*deadlineErrorStream) SetReadDeadline(time.Time) error  { return nil }
func (*deadlineErrorStream) SetWriteDeadline(time.Time) error { return nil }

func (s *deadlineSetterErrorStream) Read([]byte) (int, error) {
	s.reads++
	return 0, os.ErrDeadlineExceeded
}
func (*deadlineSetterErrorStream) Write(p []byte) (int, error) { return len(p), nil }
func (*deadlineSetterErrorStream) Close() error                { return nil }
func (s *deadlineSetterErrorStream) SetReadDeadline(time.Time) error {
	return s.err
}
func (*deadlineSetterErrorStream) SetWriteDeadline(time.Time) error { return nil }

func TestFramedStreamWriteDeadlineSetterError(t *testing.T) {
	body, err := json.Marshal(FrameRequest{Version: 1, CorrelationID: "invalid-profile", Profile: "invalid"})
	if err != nil {
		t.Fatal(err)
	}
	framed := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(framed[:4], uint32(len(body)))
	copy(framed[4:], body)
	want := errors.New("write deadline unavailable")
	stream := &writeDeadlineErrorStream{readerStream: readerStream{Reader: *bytes.NewReader(framed)}, err: want}
	if err := serveFramedStreamTransport(context.Background(), stream, nil, make(chan struct{}, 1), false, false, true, []string{"long_poll", "framed_stream"}); !errors.Is(err, want) {
		t.Fatalf("write deadline error: %v", err)
	}
	if stream.output.Len() != 0 {
		t.Fatal("wrote after failed deadline")
	}
}

type writeDeadlineErrorStream struct {
	readerStream
	err error
}

func (s *writeDeadlineErrorStream) SetWriteDeadline(time.Time) error { return s.err }
