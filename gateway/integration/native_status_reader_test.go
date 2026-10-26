package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testwait"
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
	testwait.Until(t, 2*time.Second, "the STATUS reader's owned close to end its connection", func() bool {
		status := nativeStatus(t, probe)
		if status.Observers != 0 {
			t.Fatalf("STATUS reader became an observer: %+v", status)
		}
		return status.Connections == 1
	})
}
