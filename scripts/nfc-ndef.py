#!/usr/bin/env python3
"""Encode and read the AIRC NFC Forum Type 2 NDEF carrier."""

from __future__ import annotations

import argparse
import contextlib
import importlib.metadata
import os
import re
import selectors
import signal
import stat
import subprocess
import sys
import time

MIME_TYPE = b"application/vnd.airc.bundle+json"
MAX_NDEF_BYTES = 2040
MAX_PAYLOAD_BYTES = MAX_NDEF_BYTES - 6 - len(MIME_TYPE)
MAX_INPUT_BYTES = 2040
READ_DEADLINE_SECONDS = 30.0
DEVICE_RE = re.compile(r"usb:[0-9]{3}:[0-9]{3}", re.ASCII)
INTERRUPT_SIGNALS = {signal.SIGINT, signal.SIGTERM}


class HelperError(Exception):
    pass


def _absolute_normalized(path: str) -> bool:
    return os.path.isabs(path) and os.path.normpath(path) == path


def _read_input(path: str) -> bytes:
    _require_linux()
    if not _absolute_normalized(path):
        raise HelperError("input path must be absolute and normalized")
    before = os.lstat(path)
    if not stat.S_ISREG(before.st_mode) or not 1 <= before.st_size <= MAX_INPUT_BYTES:
        raise HelperError("input must be a bounded regular file")
    flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK
    fd = os.open(path, flags)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or (before.st_dev, before.st_ino) != (info.st_dev, info.st_ino):
            raise HelperError("input must remain the same regular file")
        if info.st_size < 1 or info.st_size > MAX_INPUT_BYTES:
            raise HelperError("input size is outside the supported bound")
        chunks = bytearray()
        while len(chunks) < info.st_size:
            part = os.read(fd, info.st_size - len(chunks))
            if not part:
                raise HelperError("input changed while being read")
            chunks.extend(part)
        if os.read(fd, 1):
            raise HelperError("input changed while being read")
        return bytes(chunks)
    finally:
        os.close(fd)


def _write_output(path: str, data: bytes) -> None:
    _require_linux()
    if not data or len(data) > MAX_NDEF_BYTES:
        raise HelperError("NDEF message size is outside the supported bound")
    if not _absolute_normalized(path):
        raise HelperError("output path must be absolute and normalized")
    directory, name = os.path.split(path)
    if not name or name in (".", ".."):
        raise HelperError("output filename is invalid")
    dir_flags = os.O_RDONLY | os.O_CLOEXEC | os.O_DIRECTORY | os.O_NOFOLLOW
    dir_fd = os.open(directory, dir_flags)
    output_fd = -1
    created = False
    try:
        info = os.fstat(dir_fd)
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise HelperError("output parent must be a current-user-owned mode-0700 directory")
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
            raise HelperError("output is not a private regular file")
        offset = 0
        while offset < len(data):
            count = os.write(output_fd, data[offset:])
            if count <= 0:
                raise HelperError("output write made no progress")
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


def _encode_ndef(payload: bytes) -> bytes:
    if not payload or len(payload) > MAX_PAYLOAD_BYTES:
        raise HelperError("payload is too large for one Type 2 NDEF message")
    if len(payload) <= 255:
        result = bytes((0xD2, len(MIME_TYPE), len(payload))) + MIME_TYPE + payload
    else:
        result = bytes((0xC2, len(MIME_TYPE))) + len(payload).to_bytes(4, "big") + MIME_TYPE + payload
    if len(result) > MAX_NDEF_BYTES:
        raise HelperError("encoded NDEF message exceeds the Type 2 profile limit")
    return result


def _validate_ndef(data: bytes) -> None:
    if not 1 <= len(data) <= MAX_NDEF_BYTES or len(data) < 3:
        raise HelperError("NDEF message size is invalid")
    header, type_len = data[0], data[1]
    if header not in (0xD2, 0xC2) or type_len != len(MIME_TYPE):
        raise HelperError("NDEF message is not the expected single MIME record")
    short = bool(header & 0x10)
    cursor = 2
    if short:
        payload_len = data[cursor]
        cursor += 1
    else:
        if len(data) < cursor + 4:
            raise HelperError("NDEF payload length is truncated")
        payload_len = int.from_bytes(data[cursor : cursor + 4], "big")
        cursor += 4
    end_type = cursor + type_len
    end_payload = end_type + payload_len
    if not 1 <= payload_len <= MAX_PAYLOAD_BYTES or end_payload != len(data):
        raise HelperError("NDEF payload length is invalid")
    if data[cursor:end_type] != MIME_TYPE:
        raise HelperError("NDEF MIME type is not supported")


def _require_linux() -> None:
    if not sys.platform.startswith("linux"):
        raise HelperError("NFC helper file and reader operations are supported only on Linux")


def _read_child(device: str) -> int:
    _require_linux()
    signal.pthread_sigmask(signal.SIG_UNBLOCK, INTERRUPT_SIGNALS)
    if not DEVICE_RE.fullmatch(device):
        return 2
    try:
        if importlib.metadata.version("nfcpy") != "1.0.4":
            return 2
        with contextlib.redirect_stdout(sys.stderr):
            import nfc
            import nfc.tag.tt2

            clf = nfc.ContactlessFrontend()
            try:
                if not clf.open(device):
                    return 2
                tag = clf.connect(rdwr={"targets": ["106A"], "on-connect": lambda _tag: False})
                if not isinstance(tag, nfc.tag.tt2.Type2Tag):
                    return 2
                ndef = tag.ndef
                if ndef is None:
                    return 2
                declared_length, capacity = ndef.length, ndef.capacity
                if declared_length < 1 or declared_length > capacity or declared_length > MAX_NDEF_BYTES:
                    return 2
                raw = ndef.octets
                if len(raw) != declared_length:
                    return 2
                _validate_ndef(raw)
            finally:
                clf.close()
        view = memoryview(raw)
        written = 0
        while written < len(view):
            count = os.write(1, view[written:])
            if count <= 0:
                return 2
            written += count
        return 0
    except BaseException:
        return 2


def _read_from_device(device: str) -> bytes:
    _require_linux()
    if not DEVICE_RE.fullmatch(device):
        raise HelperError("device must be an exact usb:BBB:DDD address")
    cmd = [sys.executable, os.path.abspath(__file__), "_read-child", "--device", device]
    deadline = time.monotonic() + READ_DEADLINE_SECONDS
    proc: subprocess.Popen[bytes] | None = None
    selector: selectors.BaseSelector | None = None
    output = bytearray()
    try:
        old_mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            proc = subprocess.Popen(cmd, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, close_fds=True)
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
                raise HelperError("NFC read timed out")
            for _key, _mask in selector.select(remaining):
                try:
                    chunk = os.read(proc.stdout.fileno(), MAX_NDEF_BYTES + 1 - len(output))
                except BlockingIOError:
                    continue
                if not chunk:
                    eof = True
                    break
                output.extend(chunk)
                if len(output) > MAX_NDEF_BYTES:
                    raise HelperError("NFC reader returned an oversized message")
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise HelperError("NFC read timed out")
        try:
            status = proc.wait(timeout=remaining)
        except subprocess.TimeoutExpired as err:
            raise HelperError("NFC read timed out") from err
        if status != 0:
            raise HelperError("NFC read failed")
        _validate_ndef(bytes(output))
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


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    encode = sub.add_parser("encode")
    encode.add_argument("--input", required=True)
    encode.add_argument("--output", required=True)
    read = sub.add_parser("read")
    read.add_argument("--device", required=True)
    read.add_argument("--output", required=True)
    child = sub.add_parser("_read-child", help=argparse.SUPPRESS)
    child.add_argument("--device", required=True)
    args = parser.parse_args(argv)
    previous = {sig: signal.signal(sig, _handle_signal) for sig in INTERRUPT_SIGNALS}
    try:
        if args.command == "encode":
            _write_output(args.output, _encode_ndef(_read_input(args.input)))
        elif args.command == "read":
            _write_output(args.output, _read_from_device(args.device))
        else:
            return _read_child(args.device)
        return 0
    except HelperError as err:
        print(f"nfc-ndef: {err}", file=sys.stderr)
        return 1
    except (OSError, ValueError, KeyboardInterrupt):
        print("nfc-ndef: operation failed or interrupted", file=sys.stderr)
        return 1
    finally:
        for sig, handler in previous.items():
            signal.signal(sig, handler)


if __name__ == "__main__":
    raise SystemExit(main())
