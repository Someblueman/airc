package adapter

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/s3wire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeS3 polls one explicitly provisioned, fixed-object mailbox. It never
// deletes either object; the producer consumes and removes them in order.
func ServeS3(ctx context.Context, b config.S3, client *s3wire.Client, realm, binding string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, requests chan struct{}) error {
	if ctx == nil || client == nil || d == nil || sealer == nil || recipient == nil || realm == "" || binding == "" || cap(requests) < 1 {
		return store.ErrInvalid
	}
	b.Route.Operations = slices.Clone(b.Route.Operations)
	backoff := time.Second
	for ctx.Err() == nil {
		pass, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s3Pass(pass, b, client, realm, binding, d, sealer, recipient, requests)
		cancel()
		delay := time.Second
		if err == nil {
			backoff = time.Second
		} else {
			delay = backoff
			backoff = min(backoff*2, 15*time.Second)
		}
		if !s3Wait(ctx, delay) {
			return nil
		}
	}
	return nil
}

func s3Pass(ctx context.Context, b config.S3, client *s3wire.Client, realm, binding string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, recipient *ecdsa.PublicKey, requests chan struct{}) error {
	if _, err := client.Head(ctx, "result.jwe"); err == nil {
		return nil // A possible unread or uncertainly published result is immutable.
	} else if !errors.Is(err, s3wire.ErrNotExist) {
		return err
	}
	request, err := client.Head(ctx, "request.jwe")
	if errors.Is(err, s3wire.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !validS3Object(request, MaxRequestFrameBytes) {
		return auth.ErrAuthentication
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	case <-ctx.Done():
		return ctx.Err()
	}
	body, opened, err := client.GetRequest(ctx, request.ETag)
	defer clear(body)
	if err != nil {
		return err
	}
	if !validS3Object(opened, MaxRequestFrameBytes) || opened.ETag != request.ETag || opened.Size != request.Size || int64(len(body)) != request.Size {
		return auth.ErrAuthentication
	}
	response := admitS3(ctx, d, b, binding, body)
	token, err := sealS3Result(sealer, recipient, realm, binding, b, response)
	if err != nil || ctx.Err() != nil {
		if err != nil {
			return err
		}
		return ctx.Err()
	}
	current, err := client.Head(ctx, "request.jwe")
	if err != nil || current.ETag != request.ETag || current.Size != request.Size || !validS3Object(current, MaxRequestFrameBytes) {
		if err != nil {
			return err
		}
		return store.ErrConflict
	}
	return client.PutResult(ctx, []byte(token))
}

func validS3Object(object s3wire.Object, max int) bool {
	if object.Size < 1 || object.Size > int64(max) || len(object.ETag) < 1 || len(object.ETag) > 1024 {
		return false
	}
	for i := 0; i < len(object.ETag); i++ {
		if object.ETag[i] < 0x21 || object.ETag[i] > 0x7e {
			return false
		}
	}
	return true
}

func s3Operation(operation core.Operation) bool {
	return operation == core.Send || operation == core.ContextRead || operation == core.HistoryRead || operation == core.Search
}

func s3Wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
