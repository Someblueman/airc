"""Exclusive, deadline-bounded Linux raw UART ownership and byte I/O."""

from __future__ import annotations

import fcntl
import os
import selectors
import signal
import stat
import termios
import time

from gateway_device_io import DeviceError, INTERRUPT_SIGNALS, require_linux

_DEADLINE_SECONDS = 30.0
_READ_CHUNK = 512
_SERIAL_ERRORS = (OSError, termios.error)


def _identity(info: os.stat_result) -> tuple[int, int, int, int, int, int]:
    return (info.st_dev, info.st_ino, info.st_rdev, info.st_uid, info.st_mode, info.st_nlink)


class SerialLink:
    """Own one configured 115200 8N1 tty through context exit or close."""

    def __init__(self, path: str) -> None:
        self.path = path
        self.fd = -1
        self._selector: selectors.BaseSelector | None = None
        self._original: list[object] | None = None
        self._identity: tuple[int, int, int, int, int, int] | None = None
        self._deadline = 0.0
        self._registered_events = 0
        self._rx_buffer = bytearray()
        self._locked = False
        self._exclusive = False

    def __enter__(self) -> SerialLink:
        require_linux()
        if self.fd >= 0:
            raise DeviceError("serial link is already open")
        self._deadline = time.monotonic() + _DEADLINE_SECONDS
        self._rx_buffer.clear()
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

    def verify_path(self) -> None:
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

    def write(self, data: bytes) -> None:
        if not isinstance(data, bytes) or not 1 <= len(data) <= 1024:
            raise DeviceError("serial write must contain 1 to 1024 bytes")
        self.verify_path()
        offset = 0
        while offset < len(data):
            self._wait(selectors.EVENT_WRITE)
            try:
                count = os.write(self.fd, data[offset:])
            except (InterruptedError, BlockingIOError):
                continue
            except OSError as err:
                raise DeviceError("serial write failed") from err
            if count <= 0:
                raise DeviceError("serial write made no progress")
            offset += count
        self.verify_path()

    def read_byte(self) -> int:
        if self.fd < 0:
            raise DeviceError("serial link is closed")
        if time.monotonic() >= self._deadline:
            raise DeviceError("serial transaction timed out")
        while not self._rx_buffer:
            self.verify_path()
            self._wait(selectors.EVENT_READ)
            try:
                chunk = os.read(self.fd, _READ_CHUNK)
            except (InterruptedError, BlockingIOError):
                continue
            except OSError as err:
                raise DeviceError("serial read failed") from err
            if not chunk:
                raise DeviceError("serial device returned end of stream")
            self.verify_path()
            self._rx_buffer.extend(chunk)
        value = self._rx_buffer[0]
        del self._rx_buffer[0]
        return value

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
