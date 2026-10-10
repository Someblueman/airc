//go:build darwin && arm64

package machipc

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Request owns one received send-once reply right. Body is a private copy of
// the inline request bytes and remains valid after the listener reuses its buffer.
type Request struct {
	Body []byte
	UID  uint32
	PID  int32

	mu        sync.Mutex
	replyPort uint32
	replied   bool
}

const (
	krSuccess            = 0
	krNameExists         = 1101
	krInvalidRight       = 17
	krSendInvalidDest    = 0x10000003
	krReceiveTooLarge    = 0x10004004
	krReceiveTimedOut    = 0x10004003
	krReceiveHeaderError = 0x1000400b
	krReceiveBodyError   = 0x1000400c
	machMessageMask      = 0x00003e00
	krSendTimedOut       = 0x10000004

	portRightReceive        = 1
	portLimitInfo           = 1
	msgSend                 = 0x1
	msgReceive              = 0x2
	msgSendTimeout          = 0x10
	msgReceiveTimeout       = 0x100
	msgReceiveAuditTrailer  = 3 << 24
	dispositionMoveSendOnce = 18
	dispositionMoveSend     = 17
	trailerFormat0          = 0
	maxReceiveMillis        = 100
	maxReplyMillis          = 3000
)

type machHeader struct {
	Bits, Size, Remote, Local, Voucher uint32
	ID                                 int32
}
type auditTrailer struct {
	Type, Size, Sequence uint32
	Sender               [2]uint32
	Audit                [8]uint32
}
type machPortLimits struct{ QueueLimit uint32 }
type receivedMessage struct {
	Header  machHeader
	Body    [4 + MaxRequestBytes + 3]byte
	Trailer [128]byte
}
type Listener struct {
	name   string
	port   uint32
	mu     sync.Mutex // serializes Receive and teardown of the receive right
	closed bool
}

//go:linkname libcCall6 syscall.syscall6
//go:uintptrescapes
func libcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

//go:linkname libcCall9 syscall.syscall9
//go:uintptrescapes
func libcCall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2 uintptr, err syscall.Errno)

var libcMachMsg, libcMachPortAllocate, libcMachPortInsertRight, libcMachPortSetAttributes uintptr
var libcMachPortDeallocate, libcMachPortModRefs, libcMachPortGetRefs, libcMachMsgDestroy uintptr
var libcBootstrapRegister, libcBootstrapLookup, libcAuditTokenEUID, libcAuditTokenPID uintptr

//go:cgo_import_dynamic libc_mach_msg mach_msg "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_allocate mach_port_allocate "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_insert_right mach_port_insert_right "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_set_attributes mach_port_set_attributes "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_deallocate mach_port_deallocate "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_mod_refs mach_port_mod_refs "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_port_get_refs mach_port_get_refs "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_msg_destroy mach_msg_destroy "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_bootstrap_register bootstrap_register "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_bootstrap_lookup bootstrap_look_up "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_audit_token_euid audit_token_to_euid "/usr/lib/libbsm.0.dylib"
//go:cgo_import_dynamic libc_audit_token_pid audit_token_to_pid "/usr/lib/libbsm.0.dylib"
//go:cgo_import_dynamic libc_mach_task_self mach_task_self_ "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_bootstrap_port bootstrap_port "/usr/lib/libSystem.B.dylib"

func machTaskSelf() uintptr
func bootstrapPort() uintptr

func checkKR(name string, ret uintptr, errno syscall.Errno) error {
	if ret != krSuccess || errno != 0 {
		return fmt.Errorf("%s: mach=%d errno=%v", name, ret, errno)
	}
	return nil
}

//go:uintptrescapes
func call6(fn, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, syscall.Errno) {
	r, _, e := libcCall6(fn, a1, a2, a3, a4, a5, a6)
	return r, e
}

//go:uintptrescapes
func call9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (uintptr, syscall.Errno) {
	r, _, e := libcCall9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9)
	return r, e
}
func allocateReceivePort() (uint32, error) {
	var port uint32
	r, e := call6(libcMachPortAllocate, machTaskSelf(), portRightReceive, uintptr(unsafe.Pointer(&port)), 0, 0, 0)
	if err := checkKR("mach_port_allocate", r, e); err != nil {
		return 0, err
	}
	return port, nil
}
func sendMach(header *machHeader, size uint32, timeout uint32) error {
	flags := uintptr(msgSend)
	if timeout > 0 {
		flags |= msgSendTimeout
	}
	r, e := call9(libcMachMsg, uintptr(unsafe.Pointer(header)), flags, uintptr(size), 0, 0, uintptr(timeout), 0, 0, 0)
	if r == krSendInvalidDest {
		return fmt.Errorf("mach reply port is no longer valid")
	}
	if r == krSendTimedOut {
		return context.DeadlineExceeded
	}
	return checkKR("mach_msg send", r, e)
}

// Listen registers a fresh receive right. The bootstrap service name is removed
// by destroying that right; bootstrap provides no public unregister operation.
func Listen(name string) (listener *Listener, err error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid Mach IPC service name")
	}
	port, err := allocateReceivePort()
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			status, cleanupErr := call6(libcMachPortModRefs, machTaskSelf(), uintptr(port), portRightReceive, ^uintptr(0), 0, 0)
			if cleanup := checkKR("mach_port_deallocate failed listener setup", status, cleanupErr); cleanup != nil {
				err = errors.Join(err, cleanup)
			}
		}
	}()
	limits := machPortLimits{QueueLimit: 5}
	r, e := call6(libcMachPortSetAttributes, machTaskSelf(), uintptr(port), portLimitInfo, uintptr(unsafe.Pointer(&limits)), 1, 0)
	if err = checkKR("mach_port_set_attributes", r, e); err != nil {
		return nil, err
	}
	cname := append([]byte(name), 0)
	r, e = call6(libcBootstrapRegister, bootstrapPort(), uintptr(unsafe.Pointer(&cname[0])), uintptr(port), 0, 0, 0)
	if r == krNameExists {
		return nil, fmt.Errorf("%w: %s", ErrNameInUse, name)
	}
	if err = checkKR("bootstrap_register", r, e); err != nil {
		return nil, err
	}
	ok = true
	return &Listener{name: name, port: port}, nil
}

// Receive polls in bounded intervals, so cancellation and Close never leave a
// goroutine stuck in a Mach receive call.
func (l *Listener) Receive(ctx context.Context) (*Request, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil, ErrClosed
		}
		var msg receivedMessage
		r, e := call9(libcMachMsg, uintptr(unsafe.Pointer(&msg.Header)), msgReceive|msgReceiveTimeout|msgReceiveAuditTrailer, 0, unsafe.Sizeof(msg), uintptr(l.port), maxReceiveMillis, 0, 0, 0)
		l.mu.Unlock()
		rcvStatus := r &^ machMessageMask
		if rcvStatus == krReceiveTimedOut {
			continue
		}
		if rcvStatus == krReceiveTooLarge || rcvStatus == krReceiveHeaderError {
			return nil, ErrInvalid
		}
		if rcvStatus == krReceiveBodyError {
			_, _ = call6(libcMachMsgDestroy, uintptr(unsafe.Pointer(&msg.Header)), 0, 0, 0, 0, 0)
			return nil, ErrInvalid
		}
		if err := checkKR("mach_msg receive", r, e); err != nil {
			return nil, err
		}
		req, valid := parseRequest(&msg, l.port)
		if !valid {
			_, _ = call6(libcMachMsgDestroy, uintptr(unsafe.Pointer(&msg.Header)), 0, 0, 0, 0, 0)
			return nil, ErrInvalid
		}
		return req, nil
	}
}

func parseRequest(msg *receivedMessage, listenPort uint32) (*Request, bool) {
	h := msg.Header
	if h.Size < uint32(unsafe.Sizeof(machHeader{}))+4 || h.Size > uint32(unsafe.Sizeof(machHeader{}))+4+MaxRequestBytes+3 {
		return nil, false
	}
	wantBits := uint32(dispositionMoveSendOnce | dispositionMoveSend<<8)
	if h.Bits != wantBits || h.Remote == 0 || h.Local != listenPort || h.Voucher != 0 || h.ID != RequestID {
		return nil, false
	}
	body := msg.Body[:h.Size-uint32(unsafe.Sizeof(machHeader{}))]
	length := uint32(body[0]) | uint32(body[1])<<8 | uint32(body[2])<<16 | uint32(body[3])<<24
	if length == 0 || length > MaxRequestBytes || uint64(4)+uint64(length) > uint64(len(body)) {
		return nil, false
	}
	padded := (4 + int(length) + 3) &^ 3
	if int(h.Size)-int(unsafe.Sizeof(machHeader{})) != padded {
		return nil, false
	}
	for _, b := range body[4+length:] {
		if b != 0 {
			return nil, false
		}
	}
	trailerOffset := (uintptr(h.Size) + 3) &^ 3
	if trailerOffset+unsafe.Sizeof(auditTrailer{}) > unsafe.Sizeof(msg.Header)+unsafe.Sizeof(msg.Body)+unsafe.Sizeof(msg.Trailer) {
		return nil, false
	}
	tr := (*auditTrailer)(unsafe.Pointer(uintptr(unsafe.Pointer(msg)) + trailerOffset))
	if tr.Type != trailerFormat0 || tr.Size != uint32(unsafe.Sizeof(auditTrailer{})) {
		return nil, false
	}
	euid, err := call6(libcAuditTokenEUID, uintptr(unsafe.Pointer(&tr.Audit)), 0, 0, 0, 0, 0)
	if err != 0 {
		return nil, false
	}
	pid, err := call6(libcAuditTokenPID, uintptr(unsafe.Pointer(&tr.Audit)), 0, 0, 0, 0, 0)
	if err != 0 {
		return nil, false
	}
	payload := append([]byte(nil), body[4:4+length]...)
	return &Request{Body: payload, UID: uint32(euid), PID: int32(pid), replyPort: h.Remote}, true
}

func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	r, e := call6(libcMachPortModRefs, machTaskSelf(), uintptr(l.port), portRightReceive, ^uintptr(0), 0, 0)
	if r == krInvalidRight {
		return nil
	}
	return checkKR("mach_port_deallocate receive", r, e)
}

func (r *Request) Reply(ctx context.Context, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replied {
		return ErrReplied
	}
	r.replied = true
	port := r.replyPort
	r.replyPort = 0
	if port == 0 {
		return ErrClosed
	}
	defer func() { runtime.KeepAlive(body) }()
	if len(body) == 0 || len(body) > MaxResponseBytes {
		return errors.Join(ErrInvalid, releaseReplyPort(port))
	}
	delay := uint32(maxReplyMillis)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.Join(context.DeadlineExceeded, releaseReplyPort(port))
		}
		if remaining < time.Duration(delay)*time.Millisecond {
			delay = uint32(remaining / time.Millisecond)
			if delay < 1 {
				delay = 1
			}
		}
	}
	var wire struct {
		Header machHeader
		Body   [4 + MaxResponseBytes + 3]byte
	}
	wire.Header = machHeader{Bits: uint32(dispositionMoveSendOnce), Remote: port, ID: ResponseID}
	binaryPutUint32(wire.Body[:4], uint32(len(body)))
	copy(wire.Body[4:], body)
	size := int(unsafe.Sizeof(machHeader{})) + 4 + len(body)
	size = (size + 3) &^ 3
	wire.Header.Size = uint32(size)
	if err := sendMach(&wire.Header, uint32(size), delay); err != nil {
		// Failed sends pseudo-receive the moved right into the message header;
		// the kernel may have rewritten its name, so destroy the returned header.
		_, _ = call6(libcMachMsgDestroy, uintptr(unsafe.Pointer(&wire.Header)), 0, 0, 0, 0, 0)
		return err
	}
	return nil
}
func releaseReplyPort(port uint32) error {
	status, err := call6(libcMachPortDeallocate, machTaskSelf(), uintptr(port), 0, 0, 0, 0)
	return checkKR("mach_port_deallocate reply", status, err)
}

func (r *Request) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replied {
		return nil
	}
	r.replied = true
	if r.replyPort == 0 {
		return nil
	}
	port := r.replyPort
	r.replyPort = 0
	ret, err := call6(libcMachPortDeallocate, machTaskSelf(), uintptr(port), 0, 0, 0, 0)
	return checkKR("mach_port_deallocate reply", ret, err)
}
func binaryPutUint32(dst []byte, v uint32) {
	dst[0] = byte(v)
	dst[1] = byte(v >> 8)
	dst[2] = byte(v >> 16)
	dst[3] = byte(v >> 24)
}
