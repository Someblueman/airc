"""Exec a pinned minimodem process for one bounded acoustic transfer."""

from __future__ import annotations

import hashlib
import hmac
import os
import re
import signal
import stat
import sys
from typing import NoReturn

from gateway_device_io import DeviceError, require_linux, unblock_child_interrupts

_MAX_MODEM_BYTES = 16 * 1024 * 1024
_MAX_FRAME_BYTES = 2810
_DEVICE_RE = re.compile(r"plughw:(0|[1-9][0-9]{0,2}),(0|[1-9][0-9]{0,2})\Z", re.ASCII)
_DIGEST_RE = re.compile(r"[0-9a-f]{64}\Z", re.ASCII)


def _metadata(info: os.stat_result) -> tuple[int, ...]:
    return (
        info.st_dev,
        info.st_ino,
        info.st_mode,
        info.st_uid,
        info.st_gid,
        info.st_size,
        info.st_mtime_ns,
        info.st_ctime_ns,
    )


def _validate_device(device: str) -> None:
    if len(device.encode("ascii", errors="ignore")) > 31 or not device.isascii():
        raise DeviceError("ALSA device selector must be at most 31 ASCII bytes")
    match = _DEVICE_RE.fullmatch(device)
    if match is None or any(int(value) > 255 for value in match.groups()):
        raise DeviceError("ALSA device must be canonical plughw:N,M with N and M from 0 to 255")


def _open_modem(path: str, expected_digest: str) -> int:
    if not os.path.isabs(path) or os.path.normpath(path) != path or os.path.realpath(path) != path:
        raise DeviceError("minimodem path must be absolute, normalized, and contain no symlinks")
    if _DIGEST_RE.fullmatch(expected_digest) is None:
        raise DeviceError("minimodem digest must be lowercase SHA-256 hex")

    try:
        before = os.lstat(path)
    except OSError as err:
        raise DeviceError("cannot inspect pinned minimodem executable") from err
    if not stat.S_ISREG(before.st_mode):
        raise DeviceError("minimodem must be a regular executable file")

    flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK
    try:
        fd = os.open(path, flags)
    except OSError as err:
        raise DeviceError("cannot open pinned minimodem executable") from err
    try:
        opened = os.fstat(fd)
        if _metadata(before) != _metadata(opened):
            raise DeviceError("minimodem changed while it was opened")
        if (
            not stat.S_ISREG(opened.st_mode)
            or opened.st_uid not in (0, os.geteuid())
            or opened.st_mode & 0o022
            or opened.st_mode & 0o6000
            or not opened.st_mode & 0o111
            or not 1 <= opened.st_size <= _MAX_MODEM_BYTES
        ):
            raise DeviceError("minimodem ownership, permissions, or size is unsafe")
        if os.pread(fd, 4, 0) != b"\x7fELF":
            raise DeviceError("minimodem must be a native ELF executable")

        digest = hashlib.sha256()
        remaining = opened.st_size
        while remaining:
            chunk = os.read(fd, min(64 * 1024, remaining))
            if not chunk:
                raise DeviceError("minimodem changed while it was hashed")
            digest.update(chunk)
            remaining -= len(chunk)
        if os.read(fd, 1):
            raise DeviceError("minimodem changed while it was hashed")
        after = os.fstat(fd)
        try:
            path_after = os.lstat(path)
        except OSError as err:
            raise DeviceError("minimodem path changed while it was hashed") from err
        if _metadata(opened) != _metadata(after) or _metadata(after) != _metadata(path_after):
            raise DeviceError("minimodem changed while it was hashed")
        if not hmac.compare_digest(digest.hexdigest(), expected_digest):
            raise DeviceError("minimodem digest does not match the provisioned value")
        os.lseek(fd, 0, os.SEEK_SET)
        return fd
    except OSError as err:
        os.close(fd)
        raise DeviceError("cannot safely verify pinned minimodem executable") from err
    except BaseException:
        os.close(fd)
        raise


def _install_stdin(frame: bytes | None) -> None:
    source = -1
    try:
        if frame is None:
            source = os.open(os.devnull, os.O_RDONLY | os.O_CLOEXEC)
        else:
            if not 1 <= len(frame) <= _MAX_FRAME_BYTES:
                raise DeviceError("transmit frame must be between 1 and 2810 bytes")
            if not hasattr(os, "memfd_create"):
                raise DeviceError("Linux memfd support is required for transmit mode")
            source = os.memfd_create("airc-acoustic-frame", os.MFD_CLOEXEC)
            view = memoryview(frame)
            offset = 0
            while offset < len(view):
                count = os.write(source, view[offset:])
                if count <= 0:
                    raise DeviceError("transmit frame staging made no progress")
                offset += count
            os.lseek(source, 0, os.SEEK_SET)
        os.dup2(source, sys.stdin.fileno(), inheritable=True)
    except OSError as err:
        raise DeviceError("cannot prepare modem standard input") from err
    finally:
        if source >= 0 and source != sys.stdin.fileno():
            os.close(source)


def exec_modem(path: str, digest: str, device: str, mode: str, frame: bytes | None) -> NoReturn:
    """Replace the supervised child with the pinned modem binary."""
    require_linux()
    unblock_child_interrupts()
    signal.signal(signal.SIGALRM, signal.SIG_DFL)
    signal.alarm(30)

    _validate_device(device)
    if mode not in ("rx", "tx"):
        raise DeviceError("mode must be rx or tx")
    if (mode == "rx" and frame is not None) or (mode == "tx" and frame is None):
        raise DeviceError("frame must be absent for receive and present for transmit")

    modem_fd = _open_modem(path, digest)
    try:
        _install_stdin(frame)
        direction = ["--rx", "--rx-one"] if mode == "rx" else ["--tx", "--volume=0.5"]
        argv = [
            "minimodem",
            "--quiet",
            "--ascii",
            "--startbits=1",
            "--stopbits=1",
            "--samplerate=48000",
            "--mark=1200",
            "--space=2200",
            f"--alsa={device}",
            *direction,
            "1200",
        ]
        # minimodem uses SIGALRM for transmitter carrier shutdown; the parent
        # owns the hard process deadline once this exec replaces the child.
        signal.alarm(0)
        os.execve(modem_fd, argv, os.environ)
    except OSError as err:
        os.close(modem_fd)
        raise DeviceError("cannot exec pinned minimodem binary") from err
    except BaseException:
        os.close(modem_fd)
        raise
    raise DeviceError("minimodem exec returned unexpectedly")
