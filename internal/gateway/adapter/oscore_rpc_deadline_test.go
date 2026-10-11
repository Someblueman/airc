package adapter

import (
	"encoding/binary"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

func TestOSCORERPCWriteDeadlineBoundsStalledNativePeer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner, peer := net.Pipe()
		defer peer.Close()
		done := make(chan error, 1)
		go func() {
			done <- ServeOSCORERPC(t.Context(), owner, new(dispatch.Dispatcher), make(chan struct{}, 1), auth.OSCOREOwner{Realm: "fixture", Principal: "device", Binding: "backend"}, nil)
		}()
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], 2)
		if _, err := peer.Write(append(header[:], []byte("{}")...)); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		err := <-done
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() || time.Since(started) != 3*time.Second {
			t.Fatal("stalled native write was not bounded", err, time.Since(started))
		}
	})
}

func TestOSCORERPCReadDeadlineCoversHeaderAndBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner, peer := net.Pipe()
		defer peer.Close()
		done := make(chan error, 1)
		go func() {
			done <- ServeOSCORERPC(t.Context(), owner, new(dispatch.Dispatcher), make(chan struct{}, 1), auth.OSCOREOwner{Realm: "fixture", Principal: "device", Binding: "backend"}, nil)
		}()
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], 2)
		started := time.Now()
		if _, err := peer.Write(header[:1]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if _, err := peer.Write(header[1:]); err != nil {
			t.Fatal(err)
		}
		if _, err := peer.Write([]byte{'{'}); err != nil {
			t.Fatal(err)
		}
		err := <-done
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() || time.Since(started) != 3*time.Second {
			t.Fatal("partial native read reset its deadline", err, time.Since(started))
		}
	})
}
