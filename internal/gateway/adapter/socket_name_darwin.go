package adapter

import "golang.org/x/sys/unix"

func socketEndpointsUnnamed(_ int, local, peer string) (bool, error) {
	return local == "" && peer == "", nil
}

// socketPeerUID is the effective UID of a connected Unix socket's peer.
func socketPeerUID(fd int) (uint32, error) {
	peer, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	if peer == nil {
		return 0, errUnixPeerUID
	}
	return peer.Uid, nil
}
