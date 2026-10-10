"""Pinned rdma-core rsocket bindings for the bounded Linux bridge."""

from __future__ import annotations

import ctypes
import errno
import fcntl
import hashlib
import ipaddress
import os
import platform
import re
import signal
import socket
import stat
import sys
from collections.abc import Iterable

from gateway_device_io import DeviceError, require_linux


MAX_LIBRARY_BYTES = 16 << 20
MAX_TRANSFER = 65536
MAX_POLL_FDS = 2
MAX_POLL_TIMEOUT_MS = 100
FD_CLOEXEC = fcntl.FD_CLOEXEC
SOL_RDMA = 0x10000
RDMA_SQSIZE = 0
RDMA_RQSIZE = 1
RDMA_INLINE = 2
RDMA_IOMAPSIZE = 3
SO_RCVBUF = socket.SO_RCVBUF
SO_SNDBUF = socket.SO_SNDBUF
SOL_SOCKET = socket.SOL_SOCKET
SOCKET_BUFFER_SET = 32768
SOCKET_BUFFER_GET = 65536
QUEUE_OPTIONS = ((RDMA_SQSIZE, 128), (RDMA_RQSIZE, 128), (RDMA_INLINE, 0), (RDMA_IOMAPSIZE, 0))
RETRY_ERRNOS = {errno.EAGAIN, errno.EINTR}
CONNECT_PENDING_ERRNOS = RETRY_ERRNOS | {errno.EINPROGRESS, errno.EALREADY}
INTERRUPT_SIGNALS = {signal.SIGINT, signal.SIGTERM}


class SockAddrIn(ctypes.Structure):
    _fields_ = [
        ("sin_family", ctypes.c_ushort),
        ("sin_port", ctypes.c_ushort),
        ("sin_addr", ctypes.c_ubyte * 4),
        ("sin_zero", ctypes.c_ubyte * 8),
    ]


class PollFD(ctypes.Structure):
    _fields_ = [("fd", ctypes.c_int), ("events", ctypes.c_short), ("revents", ctypes.c_short)]


def _check_abi() -> int:
    machine = platform.machine()
    expected_machine = {"x86_64": 62, "aarch64": 183}.get(machine)
    if expected_machine is None or sys.byteorder != "little" or sys.maxsize <= 2**32:
        raise DeviceError("rsocket helper supports only little-endian Linux x86_64 and aarch64")
    if ctypes.sizeof(ctypes.c_ulong) != 8 or ctypes.sizeof(ctypes.c_size_t) != 8:
        raise DeviceError("unsupported Linux ABI")
    if ctypes.sizeof(SockAddrIn) != 16 or ctypes.sizeof(PollFD) != 8:
        raise DeviceError("unexpected Linux socket structure layout")
    if ctypes.sizeof(ctypes.c_uint) != 4:
        raise DeviceError("unexpected Linux socklen_t layout")
    return expected_machine


def _identity(info: os.stat_result) -> tuple[int, ...]:
    return (
        info.st_dev,
        info.st_ino,
        info.st_uid,
        info.st_mode,
        info.st_size,
        info.st_mtime_ns,
        info.st_ctime_ns,
    )


def _validate_library(path: str) -> tuple[int, bytes]:
    require_linux()
    if not isinstance(path, str) or not os.path.isabs(path) or os.path.normpath(path) != path or os.path.realpath(path) != path:
        raise DeviceError("library path must be canonical and absolute")
    try:
        before_path = os.lstat(path)
    except OSError as err:
        raise DeviceError("could not inspect pinned rsocket library") from err
    mode = before_path.st_mode
    if not stat.S_ISREG(mode) or before_path.st_size < 20 or before_path.st_size > MAX_LIBRARY_BYTES:
        raise DeviceError("rsocket library must be a bounded regular ELF file")
    if before_path.st_uid not in (0, os.geteuid()) or mode & 0o022 or mode & (stat.S_ISUID | stat.S_ISGID):
        raise DeviceError("rsocket library ownership or mode is unsafe")

    fd = -1
    try:
        flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK
        mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            fd = os.open(path, flags)
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, mask)
        before_fd = os.fstat(fd)
        if _identity(before_fd) != _identity(before_path):
            raise DeviceError("rsocket library changed while opening")
        data = bytearray()
        while len(data) < before_fd.st_size:
            chunk = os.read(fd, min(1 << 20, before_fd.st_size - len(data)))
            if not chunk:
                raise DeviceError("rsocket library changed while reading")
            data.extend(chunk)
        if os.read(fd, 1):
            raise DeviceError("rsocket library changed while reading")
        after_fd = os.fstat(fd)
        try:
            after_path = os.lstat(path)
        except OSError as err:
            raise DeviceError("rsocket library path changed") from err
        if _identity(after_fd) != _identity(before_fd) or _identity(after_path) != _identity(before_fd):
            raise DeviceError("rsocket library changed while reading")
        if os.path.realpath(path) != path:
            raise DeviceError("rsocket library path is no longer canonical")
        if data[:4] != b"\x7fELF" or data[4] != 2 or data[5] != 1 or data[18:20] != _check_abi().to_bytes(2, "little"):
            raise DeviceError("rsocket library ELF architecture does not match this host")
        return fd, bytes(data)
    except BaseException:
        if fd >= 0:
            mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
            try:
                os.close(fd)
            finally:
                signal.pthread_sigmask(signal.SIG_SETMASK, mask)
        raise


class RDMALibrary:
    """Hash-pin one rsocket shared library and own every rsocket descriptor."""

    def __init__(self, path: str, digest: str):
        self._path = path
        self._digest = digest
        self._memfd = -1
        self._library: ctypes.CDLL | None = None
        self._fds: set[int] = set()
        self._entered = False

    def __enter__(self) -> RDMALibrary:
        require_linux()
        _check_abi()
        if self._entered or self._memfd >= 0:
            raise DeviceError("rsocket library context is already active")
        if not isinstance(self._digest, str) or re.fullmatch(r"[0-9a-f]{64}", self._digest) is None:
            raise DeviceError("library digest must be lowercase SHA-256")
        source_fd, content = _validate_library(self._path)
        try:
            opened_fd = source_fd
            source_fd = -1
            os.close(opened_fd)
            if hashlib.sha256(content).hexdigest() != self._digest:
                raise DeviceError("rsocket library digest mismatch")
            mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
            try:
                self._memfd = os.memfd_create("airc-rsocket", os.MFD_CLOEXEC | os.MFD_ALLOW_SEALING)
            finally:
                signal.pthread_sigmask(signal.SIG_SETMASK, mask)
            offset = 0
            while offset < len(content):
                count = os.write(self._memfd, content[offset:])
                if count <= 0:
                    raise DeviceError("could not pin rsocket library")
                offset += count
            seals = fcntl.F_SEAL_WRITE | fcntl.F_SEAL_GROW | fcntl.F_SEAL_SHRINK | fcntl.F_SEAL_SEAL
            fcntl.fcntl(self._memfd, fcntl.F_ADD_SEALS, seals)
            if fcntl.fcntl(self._memfd, fcntl.F_GET_SEALS) & seals != seals:
                raise DeviceError("rsocket library pin could not be sealed")
            try:
                self._library = ctypes.CDLL(f"/proc/self/fd/{self._memfd}", use_errno=True)
            except OSError as err:
                raise DeviceError("could not load pinned rsocket library") from err
            self._declare_functions()
            self._entered = True
            return self
        except BaseException:
            self._entered = False
            self._library = None
            if self._memfd >= 0:
                mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
                try:
                    os.close(self._memfd)
                finally:
                    signal.pthread_sigmask(signal.SIG_SETMASK, mask)
                self._memfd = -1
            raise
        finally:
            if source_fd >= 0:
                os.close(source_fd)

    def _declare_functions(self) -> None:
        assert self._library is not None
        lib = self._library
        lib.rsocket.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_int]
        lib.rsocket.restype = ctypes.c_int
        lib.rbind.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_uint]
        lib.rbind.restype = ctypes.c_int
        lib.rlisten.argtypes = [ctypes.c_int, ctypes.c_int]
        lib.rlisten.restype = ctypes.c_int
        lib.raccept.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.POINTER(ctypes.c_uint)]
        lib.raccept.restype = ctypes.c_int
        lib.rconnect.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_uint]
        lib.rconnect.restype = ctypes.c_int
        lib.rshutdown.argtypes = [ctypes.c_int, ctypes.c_int]
        lib.rshutdown.restype = ctypes.c_int
        lib.rclose.argtypes = [ctypes.c_int]
        lib.rclose.restype = ctypes.c_int
        lib.rrecv.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_size_t, ctypes.c_int]
        lib.rrecv.restype = ctypes.c_ssize_t
        lib.rsend.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_size_t, ctypes.c_int]
        lib.rsend.restype = ctypes.c_ssize_t
        lib.rpoll.argtypes = [ctypes.POINTER(PollFD), ctypes.c_ulong, ctypes.c_int]
        lib.rpoll.restype = ctypes.c_int
        lib.rsetsockopt.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_int, ctypes.c_void_p, ctypes.c_uint]
        lib.rsetsockopt.restype = ctypes.c_int
        lib.rgetsockopt.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_int, ctypes.c_void_p, ctypes.POINTER(ctypes.c_uint)]
        lib.rgetsockopt.restype = ctypes.c_int
        lib.rfcntl.argtypes = [ctypes.c_int, ctypes.c_int]
        lib.rfcntl.restype = ctypes.c_int

    def _ensure_open(self) -> ctypes.CDLL:
        if not self._entered or self._library is None:
            raise DeviceError("rsocket library is closed")
        return self._library

    def _track(self, fd: int) -> None:
        if fd < 0 or fd in self._fds:
            raise DeviceError("rsocket returned an invalid descriptor")
        self._fds.add(fd)

    def _check_fd(self, fd: int) -> None:
        self._ensure_open()
        if fd not in self._fds:
            raise DeviceError("descriptor is not owned by this rsocket library")

    def _fd_flags(self, fd: int) -> None:
        lib = self._ensure_open()
        flags = lib.rfcntl(fd, fcntl.F_GETFL)
        if flags < 0:
            raise DeviceError("could not inspect rsocket descriptor flags")
        if lib.rfcntl(fd, fcntl.F_SETFL, flags | os.O_NONBLOCK) < 0:
            raise DeviceError("could not set nonblocking rsocket descriptor")
        descriptor_flags = lib.rfcntl(fd, fcntl.F_GETFD)
        if descriptor_flags < 0 or lib.rfcntl(fd, fcntl.F_SETFD, descriptor_flags | FD_CLOEXEC) < 0:
            raise DeviceError("could not set close-on-exec rsocket descriptor")
        if lib.rfcntl(fd, fcntl.F_GETFL) & os.O_NONBLOCK == 0:
            raise DeviceError("rsocket descriptor is not nonblocking")
        if lib.rfcntl(fd, fcntl.F_GETFD) & FD_CLOEXEC == 0:
            raise DeviceError("rsocket descriptor is not close-on-exec")

    def _set_option(self, fd: int, level: int, name: int, value: int) -> None:
        lib = self._ensure_open()
        option = ctypes.c_int(value)
        ctypes.set_errno(0)
        if lib.rsetsockopt(fd, level, name, ctypes.byref(option), ctypes.sizeof(option)) != 0:
            raise DeviceError("could not configure rsocket buffers")

    def _get_option(self, fd: int, level: int, name: int) -> int:
        lib = self._ensure_open()
        option = ctypes.c_int()
        length = ctypes.c_uint(ctypes.sizeof(option))
        ctypes.set_errno(0)
        if lib.rgetsockopt(fd, level, name, ctypes.byref(option), ctypes.byref(length)) != 0:
            raise DeviceError("could not verify rsocket options")
        if length.value != ctypes.sizeof(option):
            raise DeviceError("rsocket option has an unexpected size")
        return option.value

    def _configure_new_socket(self, fd: int) -> None:
        self._fd_flags(fd)
        self._set_option(fd, SOL_SOCKET, SO_RCVBUF, SOCKET_BUFFER_SET)
        self._set_option(fd, SOL_SOCKET, SO_SNDBUF, SOCKET_BUFFER_SET)
        for option, value in QUEUE_OPTIONS:
            self._set_option(fd, SOL_RDMA, option, value)
        self._verify_preconnect_options(fd)

    def _verify_buffers(self, fd: int) -> None:
        if self._get_option(fd, SOL_SOCKET, SO_RCVBUF) != SOCKET_BUFFER_GET:
            raise DeviceError("rsocket receive buffer differs from profile")
        if self._get_option(fd, SOL_SOCKET, SO_SNDBUF) != SOCKET_BUFFER_GET:
            raise DeviceError("rsocket send buffer differs from profile")

    def _verify_preconnect_options(self, fd: int) -> None:
        self._verify_buffers(fd)
        for option, value in QUEUE_OPTIONS:
            if self._get_option(fd, SOL_RDMA, option) != value:
                raise DeviceError("rsocket queue options differ from profile")

    def verify_connected(self, fd: int) -> None:
        """Check provider-negotiated values after connect completion or accept."""
        self._check_fd(fd)
        self._verify_buffers(fd)
        sqsize = self._get_option(fd, SOL_RDMA, RDMA_SQSIZE)
        rqsize = self._get_option(fd, SOL_RDMA, RDMA_RQSIZE)
        inline = self._get_option(fd, SOL_RDMA, RDMA_INLINE)
        iomap = self._get_option(fd, SOL_RDMA, RDMA_IOMAPSIZE)
        if not 16 <= sqsize <= 128 or not 16 <= rqsize <= 128:
            raise DeviceError("provider-negotiated rsocket queue size is outside profile")
        if not 0 <= inline <= 256 or iomap != 0:
            raise DeviceError("provider-negotiated rsocket options are outside profile")

    @staticmethod
    def _sockaddr(ip: str, port: int, *, allow_zero: bool = False) -> SockAddrIn:
        try:
            address = ipaddress.IPv4Address(ip)
        except (ipaddress.AddressValueError, TypeError) as err:
            raise DeviceError("rsocket endpoint must be a numeric IPv4 address") from err
        minimum = 0 if allow_zero else 1
        if str(address) != ip or type(port) is not int or not minimum <= port <= 65535:
            raise DeviceError("rsocket endpoint is not canonical")
        sockaddr = SockAddrIn()
        sockaddr.sin_family = socket.AF_INET
        sockaddr.sin_port = socket.htons(port)
        sockaddr.sin_addr[:] = address.packed
        return sockaddr

    def socket(self) -> int:
        lib = self._ensure_open()
        mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            ctypes.set_errno(0)
            fd = lib.rsocket(
                socket.AF_INET,
                socket.SOCK_STREAM | socket.SOCK_NONBLOCK | socket.SOCK_CLOEXEC,
                socket.IPPROTO_TCP,
            )
            if fd >= 0:
                self._track(fd)
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, mask)
        if fd < 0:
            raise DeviceError("could not create rsocket")
        try:
            self._configure_new_socket(fd)
            return fd
        except BaseException:
            self.close(fd)
            raise

    def bind(self, fd: int, ip: str, port: int) -> None:
        self._check_fd(fd)
        address = self._sockaddr(ip, port, allow_zero=True)
        ctypes.set_errno(0)
        if self._ensure_open().rbind(fd, ctypes.byref(address), ctypes.sizeof(address)) != 0:
            raise DeviceError("rsocket bind failed")

    def listen(self, fd: int) -> None:
        self._check_fd(fd)
        ctypes.set_errno(0)
        if self._ensure_open().rlisten(fd, 1) != 0:
            raise DeviceError("rsocket listen failed")

    def accept(self, fd: int) -> tuple[int, str, int] | None:
        self._check_fd(fd)
        lib = self._ensure_open()
        address = SockAddrIn()
        length = ctypes.c_uint(ctypes.sizeof(address))
        mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            ctypes.set_errno(0)
            accepted = lib.raccept(fd, ctypes.byref(address), ctypes.byref(length))
            err = ctypes.get_errno()
            if accepted >= 0:
                self._track(accepted)
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, mask)
        if accepted < 0:
            if err in RETRY_ERRNOS:
                return None
            raise DeviceError("rsocket accept failed")
        try:
            if length.value != ctypes.sizeof(address) or address.sin_family != socket.AF_INET:
                raise DeviceError("rsocket returned an invalid IPv4 peer address")
            self._fd_flags(accepted)
            self.verify_connected(accepted)
            peer = str(ipaddress.IPv4Address(bytes(address.sin_addr)))
            return accepted, peer, socket.ntohs(address.sin_port)
        except BaseException:
            self.close(accepted)
            raise

    def connect(self, fd: int, ip: str, port: int) -> bool:
        self._check_fd(fd)
        address = self._sockaddr(ip, port)
        ctypes.set_errno(0)
        if self._ensure_open().rconnect(fd, ctypes.byref(address), ctypes.sizeof(address)) == 0:
            return True
        if ctypes.get_errno() in CONNECT_PENDING_ERRNOS:
            return False
        raise DeviceError("rsocket connect failed")

    def socket_error(self, fd: int) -> int:
        self._check_fd(fd)
        return self._get_option(fd, SOL_SOCKET, socket.SO_ERROR)

    def recv(self, fd: int, size: int) -> bytes | None:
        self._check_fd(fd)
        if type(size) is not int or not 1 <= size <= MAX_TRANSFER:
            raise DeviceError("rsocket receive size is outside its bound")
        buffer = ctypes.create_string_buffer(size)
        ctypes.set_errno(0)
        count = self._ensure_open().rrecv(fd, buffer, size, 0)
        if count < 0:
            if ctypes.get_errno() in RETRY_ERRNOS:
                return None
            raise DeviceError("rsocket receive failed")
        if count > size:
            raise DeviceError("rsocket returned an oversized receive")
        return buffer.raw[:count]

    def send(self, fd: int, data: bytes) -> int | None:
        self._check_fd(fd)
        if not isinstance(data, bytes) or len(data) > MAX_TRANSFER:
            raise DeviceError("rsocket send data is outside its bound")
        if not data:
            return 0
        buffer = ctypes.create_string_buffer(data, len(data))
        ctypes.set_errno(0)
        count = self._ensure_open().rsend(fd, buffer, len(data), socket.MSG_NOSIGNAL)
        if count < 0:
            if ctypes.get_errno() in RETRY_ERRNOS:
                return None
            raise DeviceError("rsocket send failed")
        if count > len(data):
            raise DeviceError("rsocket returned an oversized send")
        return count

    def shutdown_write(self, fd: int) -> None:
        self._check_fd(fd)
        ctypes.set_errno(0)
        if self._ensure_open().rshutdown(fd, socket.SHUT_WR) != 0:
            raise DeviceError("rsocket write shutdown failed")

    def close(self, fd: int) -> None:
        self._check_fd(fd)
        mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            self._fds.remove(fd)
            ctypes.set_errno(0)
            if self._ensure_open().rclose(fd) != 0:
                raise DeviceError("rsocket close failed")
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, mask)

    def poll(self, entries: Iterable[tuple[int, int]], timeout_ms: int) -> dict[int, int]:
        self._ensure_open()
        pairs = list(entries)
        if len(pairs) > MAX_POLL_FDS or len({fd for fd, _ in pairs}) != len(pairs):
            raise DeviceError("rsocket poll accepts at most two unique descriptors")
        if type(timeout_ms) is not int or not 0 <= timeout_ms <= MAX_POLL_TIMEOUT_MS:
            raise DeviceError("rsocket poll timeout is outside its bound")
        for fd, events in pairs:
            if type(fd) is not int or not 0 <= fd <= 0x7FFFFFFF:
                raise DeviceError("rsocket poll descriptor is invalid")
            if type(events) is not int or not 0 <= events <= 0x7FFF:
                raise DeviceError("rsocket poll event mask is invalid")
        array_type = PollFD * max(1, len(pairs))
        poll_fds = array_type()
        for index, (fd, events) in enumerate(pairs):
            poll_fds[index].fd = fd
            poll_fds[index].events = events
        ctypes.set_errno(0)
        result = self._ensure_open().rpoll(poll_fds, len(pairs), timeout_ms)
        if result < 0:
            if ctypes.get_errno() == errno.EINTR:
                return {}
            raise DeviceError("rsocket poll failed")
        return {poll_fds[index].fd: poll_fds[index].revents for index in range(len(pairs)) if poll_fds[index].revents}

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> bool:
        failure = None
        mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            for fd in tuple(self._fds):
                try:
                    self.close(fd)
                except Exception as err:
                    if failure is None:
                        failure = err
            self._entered = False
            self._library = None
            if self._memfd >= 0:
                try:
                    os.close(self._memfd)
                except OSError as err:
                    if failure is None:
                        failure = err
                self._memfd = -1
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, mask)
        if failure is not None and exc is None:
            raise DeviceError("rsocket resource cleanup failed") from failure
        return False
