#!/usr/bin/env python3
"""Encode and read the AIRC NFC Forum Type 2 NDEF carrier."""

from __future__ import annotations

import argparse
import contextlib
import importlib.metadata
import os
import re
import sys

from gateway_device_io import (
    DeviceError,
    interruption_handlers,
    read_regular,
    require_linux,
    run_child,
    unblock_child_interrupts,
    write_private,
)

MIME_TYPE = b"application/vnd.airc.bundle+json"
MAX_NDEF_BYTES = 2040
MAX_PAYLOAD_BYTES = MAX_NDEF_BYTES - 6 - len(MIME_TYPE)
MAX_INPUT_BYTES = 2040
DEVICE_RE = re.compile(r"usb:[0-9]{3}:[0-9]{3}", re.ASCII)


def _encode_ndef(payload: bytes) -> bytes:
    if not payload or len(payload) > MAX_PAYLOAD_BYTES:
        raise DeviceError("payload is too large for one Type 2 NDEF message")
    if len(payload) <= 255:
        result = bytes((0xD2, len(MIME_TYPE), len(payload))) + MIME_TYPE + payload
    else:
        result = bytes((0xC2, len(MIME_TYPE))) + len(payload).to_bytes(4, "big") + MIME_TYPE + payload
    if len(result) > MAX_NDEF_BYTES:
        raise DeviceError("encoded NDEF message exceeds the Type 2 profile limit")
    return result


def _validate_ndef(data: bytes) -> None:
    if not 1 <= len(data) <= MAX_NDEF_BYTES or len(data) < 3:
        raise DeviceError("NDEF message size is invalid")
    header, type_len = data[0], data[1]
    if header not in (0xD2, 0xC2) or type_len != len(MIME_TYPE):
        raise DeviceError("NDEF message is not the expected single MIME record")
    short = bool(header & 0x10)
    cursor = 2
    if short:
        payload_len = data[cursor]
        cursor += 1
    else:
        if len(data) < cursor + 4:
            raise DeviceError("NDEF payload length is truncated")
        payload_len = int.from_bytes(data[cursor : cursor + 4], "big")
        cursor += 4
    end_type = cursor + type_len
    end_payload = end_type + payload_len
    if not 1 <= payload_len <= MAX_PAYLOAD_BYTES or end_payload != len(data):
        raise DeviceError("NDEF payload length is invalid")
    if data[cursor:end_type] != MIME_TYPE:
        raise DeviceError("NDEF MIME type is not supported")


def _read_child(device: str) -> int:
    require_linux()
    unblock_child_interrupts()
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
    require_linux()
    if not DEVICE_RE.fullmatch(device):
        raise DeviceError("device must be an exact usb:BBB:DDD address")
    command = [sys.executable, os.path.abspath(__file__), "_read-child", "--device", device]
    data = run_child(command, MAX_NDEF_BYTES)
    _validate_ndef(data)
    return data


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
    with interruption_handlers():
        try:
            if args.command == "encode":
                write_private(args.output, _encode_ndef(read_regular(args.input, MAX_INPUT_BYTES)), MAX_NDEF_BYTES)
            elif args.command == "read":
                write_private(args.output, _read_from_device(args.device), MAX_NDEF_BYTES)
            else:
                return _read_child(args.device)
            return 0
        except DeviceError as err:
            print(f"nfc-ndef: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("nfc-ndef: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
