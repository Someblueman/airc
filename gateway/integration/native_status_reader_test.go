package gateway_test

import (
	"testing"
	"time"
)

func TestNativeStatusReaderOwnsOneNonObservingConnection(t *testing.T) {
	socket := fixture(t)
	reader := nativeStatusClient(t, socket)
	probe := nativeStatusClient(t, socket)
	for range 100 {
		if status := nativeStatus(t, reader); status.Connections != 2 || status.Observers != 0 || status.Connections > status.MaxConnections {
			t.Fatalf("STATUS polling changed native capacity: %+v", status)
		}
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := nativeStatus(t, probe)
		if status.Observers != 0 {
			t.Fatalf("STATUS reader became an observer: %+v", status)
		}
		if status.Connections == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("STATUS reader survived its owned close")
}
