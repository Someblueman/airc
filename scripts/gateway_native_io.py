"""Pinned ELF executable and bounded stdin ownership for Linux native helpers."""

from __future__ import annotations

import hashlib
import hmac
import os
import re
import stat
import sys

from gateway_device_io import DeviceError

MAX_NATIVE_BYTES = 16 * 1024 * 1024
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


def open_native(path: str, expected_digest: str) -> int:
    if not os.path.isabs(path) or os.path.normpath(path) != path or os.path.realpath(path) != path:
        raise DeviceError("native helper path must be absolute, normalized, and contain no symlinks")
    if _DIGEST_RE.fullmatch(expected_digest) is None:
        raise DeviceError("native helper digest must be lowercase SHA-256 hex")

    try:
        before = os.lstat(path)
    except OSError as err:
        raise DeviceError("cannot inspect pinned native helper executable") from err
    if not stat.S_ISREG(before.st_mode):
        raise DeviceError("native helper must be a regular executable file")

    flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK
    try:
        fd = os.open(path, flags)
    except OSError as err:
        raise DeviceError("cannot open pinned native helper executable") from err
    try:
        opened = os.fstat(fd)
        if _metadata(before) != _metadata(opened):
            raise DeviceError("native helper changed while it was opened")
        if (
            not stat.S_ISREG(opened.st_mode)
            or opened.st_uid not in (0, os.geteuid())
            or opened.st_mode & 0o022
            or opened.st_mode & 0o6000
            or not opened.st_mode & 0o111
            or not 1 <= opened.st_size <= MAX_NATIVE_BYTES
        ):
            raise DeviceError("native helper ownership, permissions, or size is unsafe")
        if os.pread(fd, 4, 0) != b"\x7fELF":
            raise DeviceError("native helper must be a native ELF executable")

        digest = hashlib.sha256()
        remaining = opened.st_size
        while remaining:
            chunk = os.read(fd, min(64 * 1024, remaining))
            if not chunk:
                raise DeviceError("native helper changed while it was hashed")
            digest.update(chunk)
            remaining -= len(chunk)
        if os.read(fd, 1):
            raise DeviceError("native helper changed while it was hashed")
        after = os.fstat(fd)
        try:
            path_after = os.lstat(path)
        except OSError as err:
            raise DeviceError("native helper path changed while it was hashed") from err
        if _metadata(opened) != _metadata(after) or _metadata(after) != _metadata(path_after):
            raise DeviceError("native helper changed while it was hashed")
        if not hmac.compare_digest(digest.hexdigest(), expected_digest):
            raise DeviceError("native helper digest does not match the provisioned value")
        os.lseek(fd, 0, os.SEEK_SET)
        return fd
    except OSError as err:
        os.close(fd)
        raise DeviceError("cannot safely verify pinned native helper executable") from err
    except BaseException:
        os.close(fd)
        raise


def install_stdin(frame: bytes | None, limit: int) -> None:
    source = -1
    try:
        if frame is None:
            source = os.open(os.devnull, os.O_RDONLY | os.O_CLOEXEC)
        else:
            if not 1 <= len(frame) <= limit:
                raise DeviceError("native input is outside its size bound")
            if not hasattr(os, "memfd_create"):
                raise DeviceError("Linux memfd support is required for transmit mode")
            source = os.memfd_create("airc-native-input", os.MFD_CLOEXEC)
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
        raise DeviceError("cannot prepare native standard input") from err
    finally:
        if source >= 0 and source != sys.stdin.fileno():
            os.close(source)
