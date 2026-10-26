package adapter

import "golang.org/x/sys/unix"

func socketEndpointsUnnamed(_ int, local, peer string) (bool, error) {
	return local == "" && peer == "", nil
}

// socketPeer is the effective UID and the process ID of a connected Unix
// socket's peer.
func socketPeer(fd int) (uint32, int, error) {
	peer, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, 0, err
	}
	if peer == nil {
		return 0, 0, errUnixPeerUID
	}
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return 0, 0, err
	}
	return peer.Uid, pid, nil
}
