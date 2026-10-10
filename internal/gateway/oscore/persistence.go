// Package oscore owns the bounded native OSCORE process boundary.
package oscore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const callbackTimeout = 3 * time.Second

var ErrProtocol = errors.New("invalid native OSCORE persistence protocol")

// ServePersistence owns and closes one private connected native-process socket.
// The caller owns and joins the child process. One socket is bound to one
// configured context, never a principal or digest supplied by a packet.
// Startup refuses missing state before sending any material. Callbacks commit
// before acknowledgement; any failure must poison the native crypto context.
func ServePersistence(ctx context.Context, conn net.Conn, s *store.Store, material *auth.OSCOREContext, realm string) error {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if material == nil || material.Owner().Realm != realm {
		return auth.ErrCredential
	}
	digest, err := material.Digest()
	if err != nil {
		return err
	}
	owner := material.Owner()
	key := store.OSCOREKey{Digest: digest, Principal: owner.Principal, Binding: owner.Binding}
	state, err := s.LoadOSCORE(ctx, key)
	if err != nil {
		return err
	}
	configuration, err := material.NativeConfig(true)
	if err != nil {
		return err
	}
	defer clear(configuration)
	if len(configuration) > 1024 {
		return ErrProtocol
	}
	header := make([]byte, 34, len(configuration)+34)
	copy(header, "AGOS1")
	binary.BigEndian.PutUint32(header[5:9], uint32(len(configuration)))
	binary.BigEndian.PutUint64(header[9:17], state.SenderEnd)
	if state.ReceiverInitialized {
		header[17] = 1
	}
	binary.BigEndian.PutUint64(header[18:26], state.ReceiverSequence)
	binary.BigEndian.PutUint64(header[26:34], state.ReceiverWindow)
	header = append(header, configuration...)
	defer clear(header)
	if err := conn.SetWriteDeadline(time.Now().Add(callbackTimeout)); err != nil {
		return err
	}
	if _, err := io.Copy(conn, bytes.NewReader(header)); err != nil {
		return err
	}
	clear(configuration)
	clear(header)
	for {
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			return err
		}
		var request [17]byte
		_, err := io.ReadFull(conn, request[:1])
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		deadline := time.Now().Add(callbackTimeout)
		if err := conn.SetReadDeadline(deadline); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, request[1:]); err != nil {
			return err
		}
		sequence, window := binary.BigEndian.Uint64(request[1:9]), binary.BigEndian.Uint64(request[9:17])
		operation, cancel := context.WithDeadline(ctx, deadline)
		switch request[0] {
		case 1:
			if window != 0 {
				err = ErrProtocol
			} else {
				err = s.ReserveOSCORESender(operation, key, sequence)
			}
		case 2:
			err = s.SaveOSCOREReceiver(operation, key, sequence, window)
		default:
			err = ErrProtocol
		}
		cancel()
		ack := byte(1)
		if err != nil {
			ack = 0
		}
		if deadlineErr := conn.SetWriteDeadline(deadline); deadlineErr != nil {
			return errors.Join(err, deadlineErr)
		}
		_, writeErr := conn.Write([]byte{ack})
		if err != nil || writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
}
