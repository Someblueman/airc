"""Bounded, exclusive Linux serial I/O for XBee API mode 2."""

from __future__ import annotations

import fcntl
import os
import secrets
import selectors
import signal
import stat
import termios
import time

from gateway_device_io import DeviceError, INTERRUPT_SIGNALS, require_linux

_ESCAPED = frozenset((0x7E, 0x7D, 0x11, 0x13))
_MAX_FRAME = 256
_MAX_NOISE = 4096
_DEADLINE_SECONDS = 30.0
_READ_CHUNK = 512
_SERIAL_ERRORS = (OSError, termios.error)


def _identity(info: os.stat_result) -> tuple[int, int, int, int, int, int]:
    return (info.st_dev, info.st_ino, info.st_rdev, info.st_uid, info.st_mode, info.st_nlink)


def _escape(data: bytes) -> bytes:
    out = bytearray()
    for value in data:
        if value in _ESCAPED:
            out.extend((0x7D, value ^ 0x20))
        else:
            out.append(value)
    return bytes(out)


class XBeeLink:
    """Own one mode-2 UART session until context exit or close."""

    def __init__(self, path: str) -> None:
        self.path = path
        self.fd = -1
        self._selector: selectors.BaseSelector | None = None
        self._original: list[object] | None = None
        self._identity: tuple[int, int, int, int, int, int] | None = None
        self._deadline = 0.0
        self._registered_events = 0
        self._rx_buffer = bytearray()
        self._next_id = secrets.randbelow(255) + 1
        self._allocated_ids = 0
        self._locked = False
        self._exclusive = False

    def __enter__(self) -> XBeeLink:
        require_linux()
        if self.fd >= 0:
            raise DeviceError("serial link is already open")
        self._deadline = time.monotonic() + _DEADLINE_SECONDS
        flags = os.O_RDWR | os.O_NOCTTY | os.O_NONBLOCK | os.O_CLOEXEC | os.O_NOFOLLOW
        fd = -1
        selector: selectors.BaseSelector | None = None
        original: list[object] | None = None
        locked = False
        exclusive = False
        opened_identity: tuple[int, int, int, int, int, int] | None = None
        mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            try:
                if not os.path.isabs(self.path) or os.path.normpath(self.path) != self.path or os.path.realpath(self.path) != self.path:
                    raise DeviceError("serial path must be absolute, normalized, and contain no symlinks")
                before = os.lstat(self.path)
                if not self._valid_tty(before):
                    raise DeviceError("serial device must be a current-user-owned mode-0600 character device")
                fd = os.open(self.path, flags)
                opened = os.fstat(fd)
                if _identity(before) != _identity(opened) or not self._valid_tty(opened):
                    raise DeviceError("serial device changed while it was opened")
                opened_identity = _identity(opened)
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                locked = True
                fcntl.ioctl(fd, termios.TIOCEXCL)
                exclusive = True
                original = termios.tcgetattr(fd)
                raw = list(original)
                raw[0] = 0
                raw[1] = 0
                raw[2] = termios.CS8 | termios.CREAD | termios.CLOCAL
                raw[3] = 0
                raw[4] = termios.B115200
                raw[5] = termios.B115200
                controls = list(raw[6])
                controls[termios.VMIN] = 1
                controls[termios.VTIME] = 0
                raw[6] = controls
                termios.tcsetattr(fd, termios.TCSANOW, raw)
                termios.tcflush(fd, termios.TCIFLUSH)
                selector = selectors.DefaultSelector()
                selector.register(fd, selectors.EVENT_READ)
                if _identity(os.lstat(self.path)) != _identity(opened):
                    raise DeviceError("serial device path changed during setup")
                self._selector = selector
                self._original = original
                self._identity = opened_identity
                self._locked = locked
                self._exclusive = exclusive
                self._registered_events = selectors.EVENT_READ
                self.fd = fd
                fd = -1
                selector = None
                original = None
                locked = False
                exclusive = False
            finally:
                signal.pthread_sigmask(signal.SIG_SETMASK, mask)
        except _SERIAL_ERRORS as err:
            if fd >= 0:
                self._rollback(fd, selector, original, exclusive, locked)
            if self.fd >= 0:
                try:
                    self.close()
                except DeviceError:
                    pass
            raise DeviceError("cannot exclusively configure serial device") from err
        except BaseException:
            if fd >= 0:
                self._rollback(fd, selector, original, exclusive, locked)
            if self.fd >= 0:
                try:
                    self.close()
                except DeviceError:
                    pass
            raise
        return self

    @staticmethod
    def _valid_tty(info: os.stat_result) -> bool:
        return (
            stat.S_ISCHR(info.st_mode)
            and info.st_uid == os.geteuid()
            and stat.S_IMODE(info.st_mode) == 0o600
        )

    @staticmethod
    def _rollback(
        fd: int,
        selector: selectors.BaseSelector | None,
        original: list[object] | None,
        exclusive: bool,
        locked: bool,
    ) -> None:
        mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            if selector is not None:
                try:
                    selector.close()
                except OSError:
                    pass
            if fd < 0:
                return
            if original is not None:
                try:
                    termios.tcsetattr(fd, termios.TCSANOW, original)
                except _SERIAL_ERRORS:
                    pass
            if exclusive:
                try:
                    fcntl.ioctl(fd, termios.TIOCNXCL)
                except OSError:
                    pass
            if locked:
                try:
                    fcntl.flock(fd, fcntl.LOCK_UN)
                except OSError:
                    pass
            try:
                os.close(fd)
            except OSError:
                pass
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, mask)

    def _verify_path(self) -> None:
        if self.fd < 0 or self._identity is None:
            raise DeviceError("serial link is closed")
        try:
            if os.path.realpath(self.path) != self.path:
                raise DeviceError("serial device path now contains a symlink")
            current = os.lstat(self.path)
            opened = os.fstat(self.fd)
        except DeviceError:
            raise
        except OSError as err:
            raise DeviceError("serial device is no longer available") from err
        if _identity(current) != self._identity or _identity(opened) != self._identity or not self._valid_tty(current):
            raise DeviceError("serial device identity or permissions changed")

    def _wait(self, event: int) -> None:
        selector = self._selector
        if selector is None:
            raise DeviceError("serial link is closed")
        if self._registered_events != event:
            try:
                selector.modify(self.fd, event)
            except OSError as err:
                raise DeviceError("cannot change serial readiness mode") from err
            self._registered_events = event
        while True:
            remaining = self._deadline - time.monotonic()
            if remaining <= 0:
                raise DeviceError("serial transaction timed out")
            try:
                ready = selector.select(remaining)
            except InterruptedError:
                continue
            except OSError as err:
                raise DeviceError("serial readiness check failed") from err
            if not ready or time.monotonic() >= self._deadline:
                raise DeviceError("serial transaction timed out")
            return

    def next_frame_id(self) -> int:
        if self.fd < 0:
            raise DeviceError("serial link is closed")
        if self._allocated_ids >= 255:
            raise DeviceError("XBee frame ID space exhausted for this link")
        frame_id = self._next_id
        self._allocated_ids += 1
        self._next_id = 1 if frame_id == 255 else frame_id + 1
        return frame_id

    def send_frame(self, payload: bytes) -> None:
        if not isinstance(payload, bytes) or not 1 <= len(payload) <= _MAX_FRAME:
            raise DeviceError("XBee API frame must contain 1 to 256 bytes")
        self._verify_path()
        length = len(payload).to_bytes(2, "big")
        checksum = bytes(((0xFF - (sum(payload) & 0xFF)) & 0xFF,))
        packet = b"\x7e" + _escape(length + payload + checksum)
        offset = 0
        while offset < len(packet):
            self._wait(selectors.EVENT_WRITE)
            try:
                count = os.write(self.fd, packet[offset:])
            except InterruptedError:
                continue
            except BlockingIOError:
                continue
            except OSError as err:
                raise DeviceError("serial frame write failed") from err
            if count <= 0:
                raise DeviceError("serial frame write made no progress")
            offset += count
        self._verify_path()

    def _read_byte(self) -> int:
        if time.monotonic() >= self._deadline:
            raise DeviceError("serial transaction timed out")
        while not self._rx_buffer:
            self._wait(selectors.EVENT_READ)
            try:
                chunk = os.read(self.fd, _READ_CHUNK)
            except InterruptedError:
                continue
            except BlockingIOError:
                continue
            except OSError as err:
                raise DeviceError("serial frame read failed") from err
            if not chunk:
                raise DeviceError("serial device returned end of stream")
            self._rx_buffer.extend(chunk)
        value = self._rx_buffer[0]
        del self._rx_buffer[0]
        return value

    def _read_unescaped(self) -> int:
        value = self._read_byte()
        if value == 0x7E:
            raise DeviceError("truncated XBee API frame")
        if value == 0x7D:
            escaped = self._read_byte()
            decoded = escaped ^ 0x20
            if decoded not in _ESCAPED:
                raise DeviceError("noncanonical XBee API escape")
            return decoded
        if value in (0x11, 0x13):
            raise DeviceError("unescaped reserved byte in XBee API frame")
        return value

    def read_frame(self) -> bytes:
        self._verify_path()
        noise = 0
        while True:
            if self._read_byte() == 0x7E:
                break
            noise += 1
            if noise > _MAX_NOISE:
                raise DeviceError("too much noise before XBee API delimiter")

        length = (self._read_unescaped() << 8) | self._read_unescaped()
        if not 1 <= length <= _MAX_FRAME:
            raise DeviceError("XBee API frame length is outside its bound")
        payload = bytes(self._read_unescaped() for _ in range(length))
        checksum = self._read_unescaped()
        if (sum(payload) + checksum) & 0xFF != 0xFF:
            raise DeviceError("XBee API frame checksum is invalid")
        self._verify_path()
        return payload

    def close(self) -> None:
        mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            fd, self.fd = self.fd, -1
            selector, self._selector = self._selector, None
            original, self._original = self._original, None
            exclusive, self._exclusive = self._exclusive, False
            locked, self._locked = self._locked, False
            self._identity = None
            errors: list[Exception] = []
            if selector is not None:
                try:
                    selector.close()
                except OSError as err:
                    errors.append(err)
            if fd >= 0:
                if original is not None:
                    try:
                        termios.tcsetattr(fd, termios.TCSANOW, original)
                    except _SERIAL_ERRORS as err:
                        errors.append(err)
                if exclusive:
                    try:
                        fcntl.ioctl(fd, termios.TIOCNXCL)
                    except OSError as err:
                        errors.append(err)
                if locked:
                    try:
                        fcntl.flock(fd, fcntl.LOCK_UN)
                    except OSError as err:
                        errors.append(err)
                try:
                    os.close(fd)
                except OSError as err:
                    errors.append(err)
            if errors:
                raise DeviceError("could not fully restore serial device state") from errors[0]
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, mask)

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> bool:
        try:
            self.close()
        except DeviceError:
            if exc_type is None:
                raise
        return False
