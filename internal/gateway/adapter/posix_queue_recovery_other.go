//go:build !linux || (!arm64 && !amd64)

package adapter

func CurrentPOSIXQueueEpoch() (POSIXQueueEpoch, error) {
	return POSIXQueueEpoch{}, ErrPOSIXQueueUnsupported
}
func ResumePOSIXQueue(string, int, POSIXQueueIdentity, POSIXQueueEpoch) (*POSIXQueue, error) {
	return nil, ErrPOSIXQueueUnsupported
}
