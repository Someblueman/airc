//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Someblueman/airc/internal/gateway/framedbridge"
	"github.com/Someblueman/airc/internal/gateway/winpipe"
)

func runChild(ctx context.Context, command string, o options) error {
	if command == "request" {
		return request(ctx, o)
	}
	return serve(ctx, o)
}

func request(ctx context.Context, o options) (result error) {
	data, err := framedbridge.Request(o.profile, o.correlation, o.input)
	if err != nil {
		return err
	}
	pipe, err := winpipe.Dial(ctx, o.pipe, o.serverSID, o.clientSID)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, pipe.Close()) }()
	if err := pipe.WriteFrame(ctx, data, framedbridge.MaxRequest); err != nil {
		return err
	}
	response, err := pipe.ReadFrame(ctx, maxResponse)
	if err != nil {
		return err
	}
	if err := framedbridge.ValidateResponse(response, o.correlation); err != nil {
		return err
	}
	// The server waits for consumption before closing its pipe instance.
	if err := pipe.WriteFrame(ctx, []byte{1}, 1); err != nil {
		return err
	}
	_, err = os.Stdout.Write(response)
	return err
}

func serve(ctx context.Context, o options) (result error) {
	pipe, err := winpipe.Accept(ctx, o.pipe, o.serverSID, o.clientSID)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, pipe.Close()) }()
	readCtx, cancelRead := context.WithTimeout(ctx, 5*time.Second)
	data, err := pipe.ReadFrame(readCtx, framedbridge.MaxRequest)
	cancelRead()
	if err != nil {
		return err
	}
	if err := pipe.VerifyClient(); err != nil {
		return err
	}
	response, err := framedbridge.Forward(ctx, o.upstream, data)
	if err != nil {
		return err
	}
	if err := pipe.WriteFrame(ctx, response, maxResponse); err != nil {
		return err
	}
	ack, err := pipe.ReadFrame(ctx, 1)
	if err != nil || len(ack) != 1 || ack[0] != 1 {
		return errors.New("client did not confirm consumption of the pipe response")
	}
	return nil
}
