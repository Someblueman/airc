//go:build (darwin && arm64) || (linux && arm64)

package adapter

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func mappedFileQueueFixture(t *testing.T) (*MappedQueue, *MappedQueue) {
	t.Helper()
	root, path := mappedFileFixture(t)
	provisionMappedFile(t, root, 0600, sharedMemoryBytes)
	file, err := root.OpenFile(mappedFileQueueName, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteAt(sharedQueueMagic[:], 4)
	if err := errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, sharedQueueLockName), nil, 0600); err != nil {
		t.Fatal(err)
	}
	consumer, err := OpenMappedFileQueue(path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = consumer.Close() })
	producer, err := OpenMappedFileQueue(path, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = producer.Close() })
	return consumer, producer
}

func TestMappedFilePacketsWholeAndFixedRoutes(t *testing.T) {
	ingress, producer := mappedFileQueueFixture(t)
	response, writer := mappedFileQueueFixture(t)
	conn, err := NewMappedFilePackets(ingress, map[string]*MappedQueue{writer.Name(): writer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	body := bytes.Repeat([]byte("p"), sharedSlotBody)
	if err := producer.WriteMessage(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadFrom(make([]byte, sharedSlotBody-1)); !errors.Is(err, errMappedQueueBuffer) {
		t.Fatal(err)
	}
	buffer := make([]byte, sharedSlotBody+1)
	n, source, err := conn.ReadFrom(buffer)
	if err != nil || n != len(body) || !bytes.Equal(buffer[:n], body) || source.String() != ingress.Name() || source.Network() != "mapped_file" {
		t.Fatal(n, source, err)
	}
	source.(*MappedFileAddr).Directory = "/changed"
	if conn.LocalAddr().String() != ingress.Name() {
		t.Fatal("source address mutation changed local address")
	}
	route := &MappedFileAddr{Directory: response.Name()}
	if !validPacketDestination(route) || clonePacketAddr(route) == route || packetWatchMode(conn) != "mapped_file_mailbox_fragments" {
		t.Fatal("route validation/clone/watch mode")
	}
	for _, addr := range []net.Addr{&MappedFileAddr{Directory: ingress.Name()}, &MappedFileAddr{Directory: "/unknown"}, &SharedMemoryAddr{Name: "/other"}, (*MappedFileAddr)(nil)} {
		if _, err := conn.WriteTo(body, addr); !errors.Is(err, ErrMappedQueueInvalid) {
			t.Fatal("invalid destination", err)
		}
	}
	if _, err := conn.WriteTo(append(body, 1), route); !errors.Is(err, ErrMappedQueueInvalid) {
		t.Fatal(err)
	}
	if n, err := conn.WriteTo(body, route); err != nil || n != len(body) {
		t.Fatal(n, err)
	}
	n, err = response.ReadMessage(t.Context(), buffer)
	if err != nil || !bytes.Equal(buffer[:n], body) {
		t.Fatal(n, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	retained, err := OpenMappedFileQueue(ingress.Name(), true)
	if err != nil {
		t.Fatal("close removed queue or retained consumer lock", err)
	}
	_ = retained.Close()
}

func TestMappedFilePacketsRejectsInvalidOwnership(t *testing.T) {
	ingress, producer := mappedFileQueueFixture(t)
	response, writer := mappedFileQueueFixture(t)
	for _, routes := range []map[string]*MappedQueue{nil, {writer.Name(): nil}, {writer.Name(): response}, {ingress.Name(): producer}, {"/wrong": writer}} {
		if _, err := NewMappedFilePackets(ingress, routes); !errors.Is(err, ErrMappedQueueInvalid) {
			t.Fatal("invalid routes", err)
		}
	}
	routes := map[string]*MappedQueue{writer.Name(): writer}
	for _, source := range []*MappedQueue{nil, producer} {
		if _, err := NewMappedFilePackets(source, routes); !errors.Is(err, ErrMappedQueueInvalid) {
			t.Fatal("invalid ingress side", err)
		}
	}
	if _, err := NewSharedMemoryPackets(ingress, routes); !errors.Is(err, ErrMappedQueueInvalid) {
		t.Fatal("shared-memory constructor accepted mapped-file carrier", err)
	}
	if err := producer.WriteMessage(t.Context(), []byte("retained")); err != nil {
		t.Fatal("failed constructor took ownership", err)
	}
}
