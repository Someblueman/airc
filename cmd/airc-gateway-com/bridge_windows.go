//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Someblueman/airc/internal/gateway/framedbridge"
	"github.com/Someblueman/airc/internal/gateway/winpipe"
	"github.com/Someblueman/airc/internal/gateway/winworker"
)

const maxObject = 4096

func exchange(ctx context.Context, command string, o options, request []byte) ([]byte, error) {
	if command == "serve" {
		return serve(ctx, o)
	}
	return invoke(ctx, o, request)
}

func serve(ctx context.Context, o options) (response []byte, result error) {
	worker, err := winworker.Start(ctx, o.worker, o.digest, o.serverSID, o.clientSID)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, worker.Close()) }()
	object, err := framedbridge.ReadFrame(worker.Output, maxObject)
	if err != nil {
		return nil, errors.New("COM worker did not publish its bounded object reference")
	}
	fmt.Fprintln(os.Stderr, "airc-gateway-com: object ready; waiting for local bootstrap connection")
	pipe, err := winpipe.Accept(ctx, o.pipe, o.serverSID, o.clientSID)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, pipe.Close()) }()
	if err := expect(ctx, pipe, 1); err != nil {
		return nil, err
	}
	if err := pipe.VerifyClient(); err != nil {
		return nil, err
	}
	if err := pipe.WriteFrame(ctx, object, maxObject); err != nil {
		return nil, err
	}
	if err := expect(ctx, pipe, 1); err != nil {
		return nil, err
	}
	request, err := framedbridge.ReadFrame(worker.Output, framedbridge.MaxRequest)
	if err != nil {
		return nil, err
	}
	response, err = framedbridge.Forward(ctx, o.upstream, request)
	if err != nil {
		return nil, err
	}
	if err := framedbridge.WriteFrame(worker.Input, response, framedbridge.MaxResponse); err != nil {
		return nil, err
	}
	// The client sends 02 only after Invoke, proxy release and native exit.
	if err := expect(ctx, pipe, 2); err != nil {
		return nil, err
	}
	if err := pipe.VerifyClient(); err != nil {
		return nil, err
	}
	if err := framedbridge.WriteFrame(worker.Input, []byte{2}, 1); err != nil {
		return nil, err
	}
	if err := worker.Finish(); err != nil {
		return nil, err
	}
	if err := pipe.WriteFrame(ctx, []byte{3}, 1); err != nil {
		return nil, err
	}
	if err := expect(ctx, pipe, 4); err != nil {
		return nil, err
	}
	return response, nil
}

func invoke(ctx context.Context, o options, request []byte) (response []byte, result error) {
	pipe, err := winpipe.Dial(ctx, o.pipe, o.serverSID, o.clientSID)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, pipe.Close()) }()
	if err := pipe.WriteFrame(ctx, []byte{1}, 1); err != nil {
		return nil, err
	}
	object, err := pipe.ReadFrame(ctx, maxObject)
	if err != nil {
		return nil, err
	}
	if err := pipe.WriteFrame(ctx, []byte{1}, 1); err != nil {
		return nil, err
	}
	worker, err := winworker.Start(ctx, o.worker, o.digest, o.serverSID, o.clientSID)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, worker.Close()) }()
	if err := framedbridge.WriteFrame(worker.Input, object, maxObject); err != nil {
		return nil, err
	}
	if err := framedbridge.WriteFrame(worker.Input, request, framedbridge.MaxRequest); err != nil {
		return nil, err
	}
	response, err = framedbridge.ReadFrame(worker.Output, framedbridge.MaxResponse)
	if err != nil {
		return nil, err
	}
	if err := framedbridge.ValidateResponse(response, o.correlation); err != nil {
		return nil, err
	}
	if err := worker.Finish(); err != nil {
		return nil, err
	}
	if err := pipe.WriteFrame(ctx, []byte{2}, 1); err != nil {
		return nil, err
	}
	if err := expect(ctx, pipe, 3); err != nil {
		return nil, err
	}
	if err := pipe.WriteFrame(ctx, []byte{4}, 1); err != nil {
		return nil, err
	}
	return response, nil
}

func expect(ctx context.Context, pipe *winpipe.Conn, value byte) error {
	data, err := pipe.ReadFrame(ctx, 1)
	if err != nil || len(data) != 1 || data[0] != value {
		return errors.New("COM bootstrap or cleanup acknowledgement failed; native outcome may be uncertain")
	}
	return nil
}
