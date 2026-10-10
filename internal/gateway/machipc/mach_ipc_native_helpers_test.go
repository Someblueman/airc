//go:build darwin && arm64

package machipc

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"
)

const (
	clientPortRightReceive        = 1
	clientDispositionCopySend     = 19
	clientDispositionMakeSendOnce = 21
	clientMsgSend                 = 1
	clientMsgSendTimeout          = 0x10
	clientMsgReceive              = 2
	clientMsgReceiveTimeout       = 0x100
	testMessageComplex            = 0x80000000
)

type nativeTestHeader struct {
	Bits, Size, Remote, Local, Voucher uint32
	ID                                 int32
}
type nativeTestMessage struct {
	Header  nativeTestHeader
	Body    [4 + MaxResponseBytes + 3]byte
	Trailer [128]byte
}

func nativeTestChild(t *testing.T, name string, input []byte, dropReply bool) ([]byte, int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMachIPCNativeHelper$")
	cmd.Env = append(os.Environ(), "MACHIPC_HELPER_NAME="+name)
	if dropReply {
		cmd.Env = append(cmd.Env, "MACHIPC_HELPER_DROP_REPLY=1")
	}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}
	if err != nil {
		return nil, 0, fmt.Errorf("native child: %w: %s", err, stderr.String())
	}
	var pid int
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "PID=") {
			pid, _ = strconv.Atoi(strings.TrimPrefix(line, "PID="))
		}
	}
	if pid == 0 {
		return nil, 0, fmt.Errorf("native child did not report pid: %q", stdout.String())
	}
	body := bytes.TrimPrefix(stdout.Bytes(), []byte("PID="+strconv.Itoa(pid)+"\n"))
	return append([]byte(nil), body...), pid, nil
}

// The helper process creates a real send-once reply right and exercises the
// same native request/reply ABI used by non-Go clients.
func TestMachIPCNativeHelper(t *testing.T) {
	name := os.Getenv("MACHIPC_HELPER_NAME")
	if name == "" {
		return
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, MaxRequestBytes+1))
	if err != nil || len(input) == 0 || len(input) > MaxRequestBytes {
		fmt.Fprintln(os.Stderr, "invalid helper input")
		os.Exit(2)
	}
	serviceName := append([]byte(name), 0)
	var service uint32
	r, e := call6(libcBootstrapLookup, bootstrapPort(), uintptr(unsafe.Pointer(&serviceName[0])), uintptr(unsafe.Pointer(&service)), 0, 0, 0)
	if err := checkKR("bootstrap_look_up", r, e); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	reply, err := allocateReceivePort()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var msg nativeTestMessage
	msg.Header = nativeTestHeader{Bits: uint32(clientDispositionCopySend | clientDispositionMakeSendOnce<<8), Remote: service, Local: reply, ID: RequestID}
	binary.LittleEndian.PutUint32(msg.Body[:4], uint32(len(input)))
	copy(msg.Body[4:], input)
	size := int(unsafe.Sizeof(msg.Header)) + 4 + len(input)
	size = (size + 3) &^ 3
	msg.Header.Size = uint32(size)
	r, e = call9(libcMachMsg, uintptr(unsafe.Pointer(&msg.Header)), clientMsgSend|clientMsgSendTimeout, uintptr(size), 0, 0, 2000, 0, 0, 0)
	if err := checkKR("native client send", r, e); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if os.Getenv("MACHIPC_HELPER_DROP_REPLY") == "1" {
		status, errno := call6(libcMachPortDeallocate, machTaskSelf(), uintptr(service), 0, 0, 0, 0)
		if err := checkKR("drop helper service send right", status, errno); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Printf("PID=%d\n", os.Getpid())
		os.Exit(0)
	}
	var response nativeTestMessage
	r, e = call9(libcMachMsg, uintptr(unsafe.Pointer(&response.Header)), clientMsgReceive|clientMsgReceiveTimeout, 0, unsafe.Sizeof(response), uintptr(reply), 3000, 0, 0, 0)
	if err := checkKR("native client receive", r, e); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if response.Header.ID != ResponseID || response.Header.Remote != 0 || response.Header.Local != reply || response.Header.Bits&testMessageComplex != 0 || response.Header.Size < uint32(unsafe.Sizeof(response.Header))+4 {
		fmt.Fprintln(os.Stderr, "bad native response header")
		os.Exit(2)
	}
	body := response.Body[:response.Header.Size-uint32(unsafe.Sizeof(response.Header))]
	n := binary.LittleEndian.Uint32(body[:4])
	if n > MaxResponseBytes || int(n)+4 > len(body) || int((4+n+3)&^3) != len(body) {
		fmt.Fprintln(os.Stderr, "bad native response length")
		os.Exit(2)
	}
	if cleanup := testDeallocate(portRightReceive, reply); cleanup != nil {
		fmt.Fprintln(os.Stderr, cleanup)
		os.Exit(2)
	}
	status, errno := call6(libcMachPortDeallocate, machTaskSelf(), uintptr(service), 0, 0, 0, 0)
	if cleanup := checkKR("test deallocate service send right", status, errno); cleanup != nil {
		fmt.Fprintln(os.Stderr, cleanup)
		os.Exit(2)
	}
	fmt.Printf("PID=%d\n", os.Getpid())
	_, _ = os.Stdout.Write(body[4 : 4+n])
	os.Exit(0)
}

func testDeallocate(right, port uint32) error {
	status, errno := call6(libcMachPortModRefs, machTaskSelf(), uintptr(port), uintptr(right), ^uintptr(0), 0, 0)
	return checkKR("test mach_port_mod_refs", status, errno)
}

type nativePortDescriptor struct {
	Name        uint32
	Pad1        uint32
	Pad2        uint16
	Disposition uint8
	Type        uint8
}
type nativeComplexMessage struct {
	Header nativeTestHeader
	Count  uint32
	Port   nativePortDescriptor
}

func testLookupService(t *testing.T, name string) uint32 {
	t.Helper()
	cname := append([]byte(name), 0)
	var port uint32
	status, errno := call6(libcBootstrapLookup, bootstrapPort(), uintptr(unsafe.Pointer(&cname[0])), uintptr(unsafe.Pointer(&port)), 0, 0, 0)
	if err := checkKR("test bootstrap_look_up", status, errno); err != nil {
		t.Fatal(err)
	}
	return port
}

func testSendRaw(t *testing.T, name string, id int32, body []byte, corruptPadding bool) uint32 {
	t.Helper()
	service := testLookupService(t, name)
	reply, err := allocateReceivePort()
	if err != nil {
		t.Fatal(err)
	}
	var msg nativeTestMessage
	msg.Header = nativeTestHeader{Bits: uint32(clientDispositionCopySend | clientDispositionMakeSendOnce<<8), Remote: service, Local: reply, ID: id}
	binary.LittleEndian.PutUint32(msg.Body[:4], uint32(len(body)))
	copy(msg.Body[4:], body)
	size := int(unsafe.Sizeof(msg.Header)) + 4 + len(body)
	size = (size + 3) &^ 3
	if corruptPadding && size > int(unsafe.Sizeof(msg.Header))+4+len(body) {
		msg.Body[4+len(body)] = 1
	}
	msg.Header.Size = uint32(size)
	status, errno := call9(libcMachMsg, uintptr(unsafe.Pointer(&msg.Header)), clientMsgSend|clientMsgSendTimeout, uintptr(size), 0, 0, 2000, 0, 0, 0)
	if err := checkKR("test raw Mach send", status, errno); err != nil {
		_ = testDeallocate(portRightReceive, reply)
		t.Fatal(err)
	}
	status, errno = call6(libcMachPortDeallocate, machTaskSelf(), uintptr(service), 0, 0, 0, 0)
	if err := checkKR("test release lookup send right", status, errno); err != nil {
		_ = testDeallocate(portRightReceive, reply)
		t.Fatal(err)
	}
	return reply
}

func testReleaseReceive(t *testing.T, port uint32) {
	t.Helper()
	if err := testDeallocate(portRightReceive, port); err != nil {
		t.Fatal(err)
	}
}

func testSendComplexPort(t *testing.T, name string, target uint32) uint32 {
	t.Helper()
	service := testLookupService(t, name)
	reply, err := allocateReceivePort()
	if err != nil {
		t.Fatal(err)
	}
	msg := nativeComplexMessage{
		Header: nativeTestHeader{Bits: uint32(clientDispositionCopySend|clientDispositionMakeSendOnce<<8) | testMessageComplex, Remote: service, Local: reply, ID: RequestID},
		Count:  1, Port: nativePortDescriptor{Name: target, Disposition: clientDispositionCopySend, Type: 0},
	}
	msg.Header.Size = uint32(unsafe.Sizeof(msg))
	status, errno := call9(libcMachMsg, uintptr(unsafe.Pointer(&msg.Header)), clientMsgSend|clientMsgSendTimeout, uintptr(msg.Header.Size), 0, 0, 2000, 0, 0, 0)
	if err := checkKR("test complex Mach send", status, errno); err != nil {
		_ = testDeallocate(portRightReceive, reply)
		t.Fatal(err)
	}
	status, errno = call6(libcMachPortDeallocate, machTaskSelf(), uintptr(service), 0, 0, 0, 0)
	if err := checkKR("test release complex lookup send right", status, errno); err != nil {
		_ = testDeallocate(portRightReceive, reply)
		t.Fatal(err)
	}
	return reply
}

func testSendRefCount(t *testing.T, port uint32) uint32 {
	t.Helper()
	var refs uint32
	status, errno := call6(libcMachPortGetRefs, machTaskSelf(), uintptr(port), 0, uintptr(unsafe.Pointer(&refs)), 0, 0)
	if err := checkKR("mach_port_get_refs", status, errno); err != nil {
		t.Fatal(err)
	}
	return refs
}
