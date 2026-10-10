"""Bounded Linux device helper file and subprocess ownership."""

from __future__ import annotations

import contextlib
import os
import selectors
import signal
import stat
import subprocess
import sys
import time
from collections.abc import Iterator

INTERRUPT_SIGNALS = {signal.SIGINT, signal.SIGTERM}


class DeviceError(Exception):
    pass


def _absolute_normalized(path: str) -> bool:
    return os.path.isabs(path) and os.path.normpath(path) == path


def read_regular(path: str, limit: int) -> bytes:
    require_linux()
    if not _absolute_normalized(path):
        raise DeviceError("input path must be absolute and normalized")
    before = os.lstat(path)
    if not stat.S_ISREG(before.st_mode) or not 1 <= before.st_size <= limit:
        raise DeviceError("input must be a bounded regular file")
    flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK
    fd = os.open(path, flags)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or (before.st_dev, before.st_ino) != (info.st_dev, info.st_ino):
            raise DeviceError("input must remain the same regular file")
        if info.st_size < 1 or info.st_size > limit:
            raise DeviceError("input size is outside the supported bound")
        chunks = bytearray()
        while len(chunks) < info.st_size:
            part = os.read(fd, info.st_size - len(chunks))
            if not part:
                raise DeviceError("input changed while being read")
            chunks.extend(part)
        if os.read(fd, 1):
            raise DeviceError("input changed while being read")
        return bytes(chunks)
    finally:
        os.close(fd)


def write_private(path: str, data: bytes, limit: int) -> None:
    require_linux()
    if not data or len(data) > limit:
        raise DeviceError("output size is outside the supported bound")
    if not _absolute_normalized(path):
        raise DeviceError("output path must be absolute and normalized")
    directory, name = os.path.split(path)
    if not name or name in (".", ".."):
        raise DeviceError("output filename is invalid")
    dir_flags = os.O_RDONLY | os.O_CLOEXEC | os.O_DIRECTORY | os.O_NOFOLLOW
    dir_fd = os.open(directory, dir_flags)
    output_fd = -1
    created = False
    try:
        info = os.fstat(dir_fd)
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise DeviceError("output parent must be a current-user-owned mode-0700 directory")
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_CLOEXEC | os.O_NOFOLLOW
        old_mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            output_fd = os.open(name, flags, 0o600, dir_fd=dir_fd)
            created = True
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, old_mask)
        os.fchmod(output_fd, 0o600)
        out_info = os.fstat(output_fd)
        if not stat.S_ISREG(out_info.st_mode) or out_info.st_uid != os.geteuid() or stat.S_IMODE(out_info.st_mode) != 0o600:
            raise DeviceError("output is not a private regular file")
        offset = 0
        while offset < len(data):
            count = os.write(output_fd, data[offset:])
            if count <= 0:
                raise DeviceError("output write made no progress")
            offset += count
    except BaseException:
        cleanup_mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            if created:
                try:
                    os.unlink(name, dir_fd=dir_fd)
                except OSError:
                    pass
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, cleanup_mask)
        raise
    finally:
        if output_fd >= 0:
            os.close(output_fd)
        os.close(dir_fd)


def require_linux() -> None:
    if not sys.platform.startswith("linux"):
        raise DeviceError("device helper operations are supported only on Linux")


def run_child(command: list[str], output_limit: int, timeout: float = 30.0) -> bytes:
    require_linux()
    deadline = time.monotonic() + timeout
    proc: subprocess.Popen[bytes] | None = None
    selector: selectors.BaseSelector | None = None
    output = bytearray()
    try:
        old_mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            proc = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, close_fds=True)
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, old_mask)
        assert proc.stdout is not None
        selector = selectors.DefaultSelector()
        os.set_blocking(proc.stdout.fileno(), False)
        selector.register(proc.stdout, selectors.EVENT_READ)
        eof = False
        while not eof:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise DeviceError("device operation timed out")
            for _key, _mask in selector.select(remaining):
                try:
                    chunk = os.read(proc.stdout.fileno(), output_limit + 1 - len(output))
                except BlockingIOError:
                    continue
                if not chunk:
                    eof = True
                    break
                output.extend(chunk)
                if len(output) > output_limit:
                    raise DeviceError("device child returned oversized output")
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise DeviceError("device operation timed out")
        try:
            status = proc.wait(timeout=remaining)
        except subprocess.TimeoutExpired as err:
            raise DeviceError("device operation timed out") from err
        if status != 0:
            raise DeviceError("device operation failed")
        return bytes(output)
    finally:
        cleanup_mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            if selector is not None:
                try:
                    selector.close()
                except OSError:
                    pass
            if proc is not None:
                if proc.stdout is not None:
                    try:
                        proc.stdout.close()
                    except OSError:
                        pass
                if proc.poll() is None:
                    try:
                        proc.terminate()
                    except ProcessLookupError:
                        pass
                    try:
                        proc.wait(timeout=1)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait()
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, cleanup_mask)


def _handle_signal(signum: int, _frame: object) -> None:
    raise KeyboardInterrupt(f"signal {signum}")


@contextlib.contextmanager
def interruption_handlers() -> Iterator[None]:
    previous = {sig: signal.signal(sig, _handle_signal) for sig in INTERRUPT_SIGNALS}
    try:
        yield
    finally:
        for sig, handler in previous.items():
            signal.signal(sig, handler)


def unblock_child_interrupts() -> None:
    require_linux()
    signal.pthread_sigmask(signal.SIG_UNBLOCK, INTERRUPT_SIGNALS)
