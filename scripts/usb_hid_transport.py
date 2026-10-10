"""Bounded Linux hidraw and configfs USB HID transport for AIRC bundles."""

from __future__ import annotations

import fcntl
import hashlib
import os
import platform
import re
import select
import stat
import struct
import time

from gateway_device_io import DeviceError, require_linux

REPORT_DESCRIPTOR = bytes.fromhex("0600ff0901a101150026ff007508954009018102c0")
REPORT_SIZE = 64
BUNDLE_LIMIT = 16 * 1024
CHUNK_SIZE = 24
MAX_FRAMES = (BUNDLE_LIMIT + CHUNK_SIZE - 1) // CHUNK_SIZE
USB_BUS = 0x03
HIDRAW_RE = re.compile(r"/dev/hidraw(?:0|[1-9][0-9]*)\Z", re.ASCII)
GADGET_NAME_RE = re.compile(r"[A-Za-z0-9_.-]{1,64}\Z", re.ASCII)
DEV_RE = re.compile(rb"([0-9]{1,10}):([0-9]{1,10})\n\Z", re.ASCII)


def _device_info(fd: int) -> None:
    if struct.calcsize("@Ihh") != 8 or struct.calcsize("@I") + 4096 != 4100:
        raise DeviceError("unsupported Linux HID ioctl structure layout")
    raw_info = bytearray(8)  # __u32 bustype, __s16 vendor, __s16 product
    fcntl.ioctl(fd, _ioc_read(ord("H"), 0x03, len(raw_info)), raw_info, True)
    if struct.unpack_from("@I", raw_info)[0] != USB_BUS:
        raise DeviceError("hidraw device is not attached over USB")

    descriptor_size = bytearray(4)
    fcntl.ioctl(fd, _ioc_read(ord("H"), 0x01, len(descriptor_size)), descriptor_size, True)
    size = struct.unpack("@i", descriptor_size)[0]
    if size != len(REPORT_DESCRIPTOR):
        raise DeviceError("hidraw report descriptor has an unexpected size")

    descriptor = bytearray(4 + 4096)  # hidraw_report_descriptor: __u32 size + value[4096]
    struct.pack_into("@I", descriptor, 0, size)
    fcntl.ioctl(fd, _ioc_read(ord("H"), 0x02, len(descriptor)), descriptor, True)
    if descriptor[4 : 4 + size] != REPORT_DESCRIPTOR:
        raise DeviceError("hidraw report descriptor does not match the AIRC profile")


def _ioc_read(type_: int, number: int, size: int) -> int:
    # Linux asm-generic ioctl encoding: _IOC(_IOC_READ, type, nr, size).
    return (2 << 30) | (size << 16) | (type_ << 8) | number


def _check_char_path(path: str, *, uid: int, expected_rdev: int | None = None) -> os.stat_result:
    if os.path.realpath(path) != path:
        raise DeviceError("device path must not contain symlinks")
    before = os.lstat(path)
    if not stat.S_ISCHR(before.st_mode) or before.st_uid != uid or stat.S_IMODE(before.st_mode) != 0o600:
        raise DeviceError("device node must be a current-user-owned mode-0600 character device")
    if expected_rdev is not None and before.st_rdev != expected_rdev:
        raise DeviceError("device node does not match the configured HID function")
    return before


def _same_char_path(path: str, original: os.stat_result, fd: int) -> None:
    current = os.lstat(path)
    opened = os.fstat(fd)
    identity = (original.st_dev, original.st_ino, original.st_rdev)
    if not stat.S_ISCHR(current.st_mode) or not stat.S_ISCHR(opened.st_mode):
        raise DeviceError("device path is no longer a character device")
    if (current.st_dev, current.st_ino, current.st_rdev) != identity:
        raise DeviceError("device path changed while in use")
    if (opened.st_dev, opened.st_ino, opened.st_rdev) != identity:
        raise DeviceError("opened device does not match its path")
    if current.st_uid != os.geteuid() or opened.st_uid != os.geteuid() or stat.S_IMODE(current.st_mode) != 0o600 or stat.S_IMODE(opened.st_mode) != 0o600:
        raise DeviceError("device ownership or permissions changed while in use")


def _open_hidraw(device: str) -> tuple[int, os.stat_result]:
    _require_supported_linux()
    if not isinstance(device, str) or not HIDRAW_RE.fullmatch(device):
        raise DeviceError("device must be a canonical /dev/hidrawN path")
    before = _check_char_path(device, uid=os.geteuid())
    flags = os.O_RDONLY | os.O_NONBLOCK | os.O_CLOEXEC | os.O_NOFOLLOW
    fd = os.open(device, flags)
    try:
        _same_char_path(device, before, fd)
        info = os.fstat(fd)
        if info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o600:
            raise DeviceError("hidraw device permissions changed while opening")
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        _device_info(fd)
        _same_char_path(device, before, fd)
        return fd, before
    except BaseException:
        os.close(fd)
        raise


def _read_report(fd: int, deadline: float) -> bytes:
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise DeviceError("HID report read timed out")
        try:
            readable, _, _ = select.select([fd], [], [], remaining)
        except InterruptedError:
            continue
        if not readable:
            raise DeviceError("HID report read timed out")
        try:
            report = os.read(fd, REPORT_SIZE + 1)
        except BlockingIOError:
            continue
        except InterruptedError:
            continue
        if len(report) != REPORT_SIZE:
            raise DeviceError("HID report has an invalid length")
        return report


def _collect_bundle(fd: int, deadline: float) -> bytes:
    expected_index = 0
    total: int | None = None
    digest: bytes | None = None
    collected = bytearray()
    expected_frames: int | None = None

    while expected_frames is None or expected_index < expected_frames:
        report = _read_report(fd, deadline)
        if report[:4] != b"AHI1":
            raise DeviceError("HID report has invalid AIRC framing")
        frame_total, frame_index = struct.unpack_from("!HH", report, 4)
        frame_digest = report[8:40]
        if not 1 <= frame_total <= BUNDLE_LIMIT or frame_index >= MAX_FRAMES:
            raise DeviceError("HID report has invalid bundle bounds")
        if frame_index != expected_index:
            raise DeviceError("HID reports are missing, duplicated, or out of order")
        if total is None:
            if frame_index != 0:
                raise DeviceError("first HID report must have index zero")
            total = frame_total
            digest = frame_digest
            expected_frames = (total + CHUNK_SIZE - 1) // CHUNK_SIZE
        elif frame_total != total or frame_digest != digest:
            raise DeviceError("HID report bundle metadata changed mid-transfer")

        assert total is not None and expected_frames is not None
        chunk_size = min(CHUNK_SIZE, total - len(collected))
        chunk = report[40:40 + chunk_size]
        padding = report[40 + chunk_size:]
        if len(chunk) != chunk_size or any(padding):
            raise DeviceError("HID report has invalid final padding")
        collected.extend(chunk)
        expected_index += 1

    assert total is not None and digest is not None
    if len(collected) != total or hashlib.sha256(collected).digest() != digest:
        raise DeviceError("HID bundle digest or length does not match")
    return bytes(collected)


def read_bundle(device: str) -> bytes:
    """Read one strictly ordered AIRC HID bundle from a pinned hidraw node."""
    fd = -1
    try:
        fd, before = _open_hidraw(device)
        bundle = _collect_bundle(fd, time.monotonic() + 30.0)
        _same_char_path(device, before, fd)
        return bundle
    except OSError as err:
        raise DeviceError("unable to read the HID device") from err
    finally:
        if fd >= 0:
            os.close(fd)


def _mount_is_configfs() -> bool:
    flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW
    fd = os.open("/proc/self/mountinfo", flags)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_size > 1 << 20:
            return False
        raw = bytearray()
        while len(raw) <= 1 << 20:
            chunk = os.read(fd, (1 << 20) + 1 - len(raw))
            if not chunk:
                break
            raw.extend(chunk)
        if len(raw) > 1 << 20:
            return False
    finally:
        os.close(fd)
    matches = []
    for line in raw.splitlines():
        halves = line.split(b" - ", 1)
        if len(halves) != 2:
            continue
        left, right = halves
        fields = left.split()
        fs_fields = right.split()
        if len(fields) > 4 and len(fs_fields) > 0 and fields[4] == b"/sys/kernel/config" and fs_fields[0] == b"configfs":
            matches.append(fs_fields[0])
    return matches == [b"configfs"]


def _require_supported_linux() -> None:
    require_linux()
    if platform.machine() not in ("x86_64", "aarch64"):
        raise DeviceError("USB HID transport supports Linux x86_64 and aarch64 only")


def _open_configfs_dir(function_path: str) -> tuple[int, os.stat_result]:
    _require_supported_linux()
    if not isinstance(function_path, str) or not function_path.startswith("/sys/kernel/config/usb_gadget/") or os.path.normpath(function_path) != function_path:
        raise DeviceError("function path must be a canonical configfs HID function path")
    parts = function_path.strip("/").split("/")
    if len(parts) != 7 or parts[:4] != ["sys", "kernel", "config", "usb_gadget"] or parts[5] != "functions":
        raise DeviceError("function path must name one USB gadget HID function")
    gadget, function = parts[4], parts[6]
    if not GADGET_NAME_RE.fullmatch(gadget) or gadget in (".", "..") or not function.startswith("hid.") or not GADGET_NAME_RE.fullmatch(function[4:]) or function[4:] in (".", ".."):
        raise DeviceError("function path contains an invalid configfs name")
    if not _mount_is_configfs():
        raise DeviceError("/sys/kernel/config is not mounted as configfs")

    current = os.open("/", os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
    try:
        for component in parts:
            next_fd = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC | os.O_NOFOLLOW, dir_fd=current)
            os.close(current)
            current = next_fd
            info = os.fstat(current)
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
                raise DeviceError("configfs path must contain root-owned protected directories")
        return current, os.fstat(current)
    except BaseException:
        os.close(current)
        raise


def _read_config_attr(dir_fd: int, name: str, limit: int) -> bytes:
    fd = os.open(name, os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW, dir_fd=dir_fd)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise DeviceError("HID configfs attribute must be a protected root-owned regular file")
        value = bytearray()
        while len(value) <= limit:
            chunk = os.read(fd, limit + 1 - len(value))
            if not chunk:
                break
            value.extend(chunk)
        if len(value) > limit:
            raise DeviceError("HID configfs attribute exceeds its bound")
        return bytes(value)
    finally:
        os.close(fd)


def _hidg_device(function_path: str) -> tuple[str, os.stat_result, os.stat_result, int]:
    dir_fd, function_info = _open_configfs_dir(function_path)
    try:
        if _read_config_attr(dir_fd, "report_desc", 4096) != REPORT_DESCRIPTOR:
            raise DeviceError("configured HID function does not use the AIRC report descriptor")
        if _read_config_attr(dir_fd, "report_length", 16) != b"64\n":
            raise DeviceError("configured HID function report length must be 64")
        if _read_config_attr(dir_fd, "protocol", 16) != b"0\n" or _read_config_attr(dir_fd, "subclass", 16) != b"0\n":
            raise DeviceError("configured HID function must be vendor-defined protocol and subclass zero")
        if _read_config_attr(dir_fd, "no_out_endpoint", 16) != b"1\n":
            raise DeviceError("configured HID function must disable its OUT endpoint")
        dev = _read_config_attr(dir_fd, "dev", 32)
        match = DEV_RE.fullmatch(dev)
        if match is None:
            raise DeviceError("HID function dev attribute is malformed")
        major, minor = (int(part) for part in match.groups())
        if f"{major}:{minor}\n".encode("ascii") != dev:
            raise DeviceError("HID function dev attribute is not canonical")
        _same_function_path(function_path, function_info, dir_fd)
        node = f"/dev/hidg{minor}"
        node_info = _check_char_path(node, uid=os.geteuid(), expected_rdev=os.makedev(major, minor))
        return node, node_info, function_info, dir_fd
    except BaseException:
        os.close(dir_fd)
        raise


def _same_function_path(path: str, original: os.stat_result, dir_fd: int) -> None:
    current = os.stat(path, follow_symlinks=False)
    opened = os.fstat(dir_fd)
    identity = (original.st_dev, original.st_ino)
    if not stat.S_ISDIR(current.st_mode) or not stat.S_ISDIR(opened.st_mode):
        raise DeviceError("HID function path is no longer a directory")
    if (current.st_dev, current.st_ino) != identity or (opened.st_dev, opened.st_ino) != identity:
        raise DeviceError("HID function path changed while in use")


def _make_reports(bundle: bytes) -> list[bytes]:
    if not isinstance(bundle, bytes) or not 1 <= len(bundle) <= BUNDLE_LIMIT:
        raise DeviceError("bundle size is outside the USB HID limit")
    digest = hashlib.sha256(bundle).digest()
    count = (len(bundle) + CHUNK_SIZE - 1) // CHUNK_SIZE
    reports = []
    for index in range(count):
        chunk = bundle[index * CHUNK_SIZE : (index + 1) * CHUNK_SIZE]
        reports.append(
            b"AHI1"
            + struct.pack("!HH", len(bundle), index)
            + digest
            + chunk.ljust(CHUNK_SIZE, b"\0")
        )
    return reports


def _write_report(fd: int, report: bytes, deadline: float) -> None:
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise DeviceError("HID report write timed out")
        try:
            _, writable, _ = select.select([], [fd], [], remaining)
        except InterruptedError:
            continue
        if not writable:
            raise DeviceError("HID report write timed out")
        try:
            written = os.write(fd, report)
        except BlockingIOError:
            continue
        except InterruptedError:
            continue
        if written != REPORT_SIZE:
            raise DeviceError("HID gadget accepted a partial report")
        return


def emit_bundle(bundle: bytes, function_path: str) -> None:
    """Write one complete opaque bundle as fixed 64-byte HID input reports."""
    _require_supported_linux()
    reports = _make_reports(bundle)
    function_fd = -1
    fd = -1
    try:
        node, before, function_info, function_fd = _hidg_device(function_path)
        flags = os.O_WRONLY | os.O_NONBLOCK | os.O_CLOEXEC | os.O_NOFOLLOW
        fd = os.open(node, flags)
        _same_char_path(node, before, fd)
        info = os.fstat(fd)
        if info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o600:
            raise DeviceError("HID gadget node permissions changed while opening")
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        deadline = time.monotonic() + 30.0
        for report in reports:
            _write_report(fd, report, deadline)
        _same_char_path(node, before, fd)
        _same_function_path(function_path, function_info, function_fd)
    except OSError as err:
        raise DeviceError("unable to write the HID gadget") from err
    finally:
        if fd >= 0:
            os.close(fd)
        if function_fd >= 0:
            os.close(function_fd)

