package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type packetOwner struct {
	conn   net.PacketConn
	driver interface {
		Serve(context.Context, net.PacketConn) error
	}
	path string
	file os.FileInfo
	once sync.Once
	err  error
}

func newUDPOwner(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	if l.AnonymousPrincipal != "" {
		driver, err := adapter.NewAnonymousUDP(d, l.ID, requests)
		if err != nil {
			return nil, err
		}
		address, err := net.ResolveUDPAddr("udp", l.Address)
		if err != nil {
			return nil, err
		}
		conn, err := net.ListenUDP("udp", address)
		if err != nil {
			return nil, err
		}
		return &packetOwner{conn: conn, driver: driver}, nil
	}
	return newPacketOwner(c, l, d, slots, requests, false)
}

func newUnixgramOwner(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*packetOwner, error) {
	if c.LocalAuthorMode(l) {
		return newUnixgramAuthorOwner(l, d, slots, requests)
	}
	return newPacketOwner(c, l, d, slots, requests, true)
}

func newPacketOwner(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}, unixgram bool) (*packetOwner, error) {
	driver, routes, err := newPacketDriver(c, l, d, slots, requests)
	if err != nil {
		return nil, err
	}
	if !unixgram {
		address, err := net.ResolveUDPAddr("udp", l.Address)
		if err != nil {
			return nil, err
		}
		if l.Protocol == "udp_lite" {
			conn, err := adapter.ListenUDPLite(address)
			if err != nil {
				return nil, err
			}
			return &packetOwner{conn: conn, driver: driver}, nil
		}
		if l.Protocol == "ipv4_multicast" {
			conn, err := adapter.ListenIPv4Multicast(address, l.IPv4Multicast.Interface)
			if err != nil {
				return nil, err
			}
			return &packetOwner{conn: conn, driver: driver}, nil
		}
		if l.Protocol == "ipv4_broadcast" {
			conn, err := adapter.ListenIPv4Broadcast(address, l.IPv4Broadcast.Interface, l.IPv4Broadcast.Subnet)
			if err != nil {
				return nil, err
			}
			return &packetOwner{conn: conn, driver: driver}, nil
		}
		if l.Protocol == "ipv6_multicast" {
			conn, err := adapter.ListenIPv6Multicast(address, l.IPv6Multicast.Interface)
			if err != nil {
				return nil, err
			}
			return &packetOwner{conn: conn, driver: driver}, nil
		}
		conn, err := net.ListenUDP("udp", address)
		if err != nil {
			return nil, err
		}
		if l.Protocol == "coap_udp" {
			return &packetOwner{conn: &adapter.CoAPUDPPackets{PacketConn: conn}, driver: driver}, nil
		}
		return &packetOwner{conn: conn, driver: driver}, nil
	}
	mailboxes := make(map[string]os.FileInfo, len(routes))
	for _, route := range routes {
		addr, ok := route.Destination.(*net.UnixAddr)
		if !ok {
			return nil, store.ErrInvalid
		}
		info, err := os.Lstat(addr.Name)
		if err != nil {
			return nil, err
		}
		mailboxes[addr.Name] = info
	}
	return listenUnixgramOwner(l.Address, driver, mailboxes)
}

func newPacketDriver(c config.Config, l config.Listener, d *dispatch.Dispatcher, slots, requests chan struct{}) (*adapter.PacketDatagrams, []adapter.PacketReturnRoute, error) {
	if c.ResponseSigningKey == nil {
		return nil, nil, store.ErrInvalid
	}
	private, err := auth.ReadResponseSigningKey(c.ResponseSigningKey.PrivateKeyFile)
	if err != nil {
		return nil, nil, err
	}
	defer clear(private)
	sealer, err := auth.NewResponseSealer(c.ResponseSigningKey.ID, private)
	if err != nil {
		return nil, nil, err
	}
	bindings := make(map[string]string, len(c.Principals))
	for _, principal := range c.Principals {
		bindings[principal.ID] = principal.Binding
	}
	routes := make([]adapter.PacketReturnRoute, 0)
	for _, route := range c.ReturnRoutes {
		protocol := l.Protocol
		if route.Protocol != protocol || route.Listener != l.ID {
			continue
		}
		recipient, err := auth.ReadResponseRecipientKey(route.RecipientPublicKeyFile)
		if err != nil {
			return nil, nil, err
		}
		var destination net.Addr
		if l.Protocol == "unixgram" {
			if err := validateUnixgramMailbox(route.Destination); err != nil {
				return nil, nil, err
			}
			destination = &net.UnixAddr{Name: route.Destination, Net: "unixgram"}
		} else if l.Protocol == "system_v_queue" {
			key, err := config.SystemVQueueKey(route.Destination)
			if err != nil {
				return nil, nil, err
			}
			destination = &adapter.SystemVQueueAddr{Key: key}
		} else if l.Protocol == "posix_mqueue" {
			destination = &adapter.POSIXQueueAddr{Name: route.Destination}
		} else if l.Protocol == "shared_memory" {
			destination = &adapter.SharedMemoryAddr{Name: route.Destination}
		} else if l.Protocol == "mapped_file" {
			destination = &adapter.MappedFileAddr{Directory: route.Destination}
		} else if l.Protocol == "icmp_v6" {
			var err error
			destination, err = adapter.ParseICMPv6Addr(route.Destination)
			if err != nil {
				return nil, nil, err
			}
		} else {
			var err error
			destination, err = net.ResolveUDPAddr("udp", route.Destination)
			if err != nil {
				return nil, nil, store.ErrInvalid
			}
		}
		routes = append(routes, adapter.PacketReturnRoute{ID: route.ID, Principal: route.Principal, Binding: bindings[route.Principal], RecipientID: route.RecipientKeyID, Destination: destination, Recipient: recipient})
	}
	driver, err := adapter.NewPacketDatagrams(d, sealer, c.Realm, routes, slots, requests)
	if err != nil {
		return nil, nil, err
	}
	return driver, routes, nil
}

func (o *packetOwner) addr() net.Addr { return o.conn.LocalAddr() }
func (o *packetOwner) close() error {
	o.once.Do(func() {
		o.err = o.conn.Close()
		if o.path != "" {
			if info, err := os.Lstat(o.path); err == nil && os.SameFile(info, o.file) {
				if err := os.Remove(o.path); o.err == nil {
					o.err = err
				}
			}
		}
	})
	return o.err
}
func (o *packetOwner) serve(ctx context.Context) error { return o.driver.Serve(ctx, o.conn) }

func listenUnixgramOwner(path string, driver *adapter.PacketDatagrams, mailboxes map[string]os.FileInfo) (*packetOwner, error) {
	if err := validateUnixgramSocketPath(path); err != nil {
		return nil, err
	}
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	created, err := os.Lstat(path)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = conn.Close()
		removeUnixgramInode(path, created)
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || !os.SameFile(info, created) || !unixgramOwner(info) {
		_ = conn.Close()
		removeUnixgramInode(path, created)
		if err != nil {
			return nil, err
		}
		return nil, store.ErrInvalid
	}
	return &packetOwner{conn: &checkedUnixPacketConn{UnixConn: conn, mailboxes: mailboxes}, driver: driver, path: path, file: info}, nil
}

func removeUnixgramInode(path string, created os.FileInfo) {
	info, err := os.Lstat(path)
	if err == nil && os.SameFile(info, created) {
		_ = os.Remove(path)
	}
}

type checkedUnixPacketConn struct {
	*net.UnixConn
	mailboxes map[string]os.FileInfo
}

func (c *checkedUnixPacketConn) WriteTo(payload []byte, destination net.Addr) (int, error) {
	addr, ok := destination.(*net.UnixAddr)
	if !ok || addr == nil || addr.Net != "unixgram" {
		return 0, store.ErrInvalid
	}
	created, ok := c.mailboxes[addr.Name]
	if !ok || !validUnixgramMailbox(addr.Name, created) {
		return 0, store.ErrInvalid
	}
	return c.UnixConn.WriteToUnix(payload, addr)
}

func validateUnixgramMailbox(path string) error {
	if err := validateUnixgramPath(path, false); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || !unixgramOwner(info) {
		return store.ErrInvalid
	}
	return nil
}

func validUnixgramMailbox(path string, created os.FileInfo) bool {
	if validateUnixgramPath(path, false) != nil {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode()&os.ModeSocket != 0 && info.Mode().Perm() == 0600 && os.SameFile(info, created) && unixgramOwner(info)
}

func validateUnixgramSocketPath(path string) error {
	return validateUnixgramPath(path, true)
}

func validateUnixgramPath(path string, absent bool) error {
	limit := 107
	if runtime.GOOS == "darwin" {
		limit = 103
	}
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") || len(path) > limit {
		return store.ErrInvalid
	}
	parent := filepath.Dir(unixgramCanonicalPath(path))
	for current := parent; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return store.ErrInvalid
		}
		if current == parent {
			if !(unixgramOwner(info) && info.Mode().Perm()&0077 == 0) && !(info.Mode()&os.ModeSticky != 0 && unixgramRootOwned(info)) {
				return store.ErrInvalid
			}
		} else if !unixgramTrustedAncestor(info) {
			return store.ErrInvalid
		}
		if current == string(filepath.Separator) {
			break
		}
	}
	if absent {
		if _, err := os.Lstat(path); err == nil {
			return store.ErrInvalid
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func unixgramCanonicalPath(path string) string {
	if runtime.GOOS != "darwin" {
		return path
	}
	if path == "/tmp" {
		return "/private/tmp"
	}
	if strings.HasPrefix(path, "/tmp/") {
		return "/private" + path
	}
	return path
}
