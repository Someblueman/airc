#!/usr/bin/env python3
"""Carry one signed bundle through a provisioned ESP32 ESP-NOW bridge."""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import sys
from typing import TYPE_CHECKING

from gateway_device_io import (
    DeviceError,
    interruption_handlers,
    read_regular,
    run_child,
    unblock_child_interrupts,
    write_private,
)

if TYPE_CHECKING:
    from gateway_serial import SerialLink

MAGIC = b"AEU1"
MAX_BUNDLE_BYTES = 2048
EMPTY_DIGEST = bytes(32)


def read_exact(link: SerialLink, length: int) -> bytes:
    if not 0 <= length <= MAX_BUNDLE_BYTES:
        raise DeviceError("bridge read exceeds its bound")
    link.verify_path()
    data = bytes(link.read_byte() for _ in range(length))
    link.verify_path()
    return data


def response(link: SerialLink, nonce: bytes, status: bytes, has_bundle: bool) -> bytes:
    header = read_exact(link, 55)
    if header[:20] != MAGIC + nonce:
        raise DeviceError("bridge reply does not match this transaction")
    length = int.from_bytes(header[21:23], "big")
    digest = header[23:55]
    if header[20:21] != status:
        raise DeviceError("bridge refused or failed the operation")
    if not has_bundle:
        if length != 0 or digest != EMPTY_DIGEST:
            raise DeviceError("bridge control reply contains unexpected data")
        return b""
    if not 1 <= length <= MAX_BUNDLE_BYTES:
        raise DeviceError("bridge returned an invalid bundle length")
    data = read_exact(link, length)
    if hashlib.sha256(data).digest() != digest:
        raise DeviceError("bridge bundle integrity check failed")
    return data


def transfer(link: SerialLink, args: argparse.Namespace, bundle: bytes) -> bytes:
    emitting = args.command == "_emit-child"
    nonce = os.urandom(16)
    local = bytes.fromhex(args.local.replace(":", ""))
    peer = bytes.fromhex(args.peer.replace(":", ""))
    header = MAGIC + nonce + local + peer + bytes((args.channel,))
    header += b"T" if emitting else b"R"
    header += len(bundle).to_bytes(2, "big")
    header += hashlib.sha256(bundle).digest() if emitting else EMPTY_DIGEST
    link.write(header)
    response(link, nonce, b"R", False)
    if emitting:
        for offset in range(0, len(bundle), 1024):
            link.write(bundle[offset : offset + 1024])
    return response(link, nonce, b"O", not emitting)


def child(args: argparse.Namespace) -> None:
    unblock_child_interrupts()
    from gateway_serial import SerialLink

    bundle = read_regular(args.input, MAX_BUNDLE_BYTES) if args.command == "_emit-child" else b""
    with SerialLink(args.device) as link:
        data = transfer(link, args, bundle)
    offset = 0
    while offset < len(data):
        count = os.write(1, data[offset:])
        if count <= 0:
            raise DeviceError("ESP-NOW child output made no progress")
        offset += count


def validate(args: argparse.Namespace) -> None:
    for value in (args.local, args.peer):
        if re.fullmatch(r"(?:[0-9A-F]{2}:){5}[0-9A-F]{2}", value) is None:
            raise DeviceError("MAC addresses must use uppercase colon-separated hexadecimal")
        raw = bytes.fromhex(value.replace(":", ""))
        if raw == bytes(6) or raw[0] & 1:
            raise DeviceError("MAC addresses must be nonzero unicast addresses")
    if args.local == args.peer or not 1 <= args.channel <= 11:
        raise DeviceError("choose distinct local/peer addresses and channel 1..11")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    flags = ("device", "local", "peer", "channel")
    for name in ("read", "emit", "_read-child", "_emit-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else name)
        for flag in flags:
            command.add_argument(f"--{flag}", required=True, type=int if flag == "channel" else str)
        if name in ("emit", "_emit-child"):
            command.add_argument("--input", required=True)
        elif name == "read":
            command.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    with interruption_handlers():
        try:
            validate(args)
            if args.command.startswith("_"):
                child(args)
                return 0
            command = [sys.executable, os.path.abspath(__file__), f"_{args.command}-child"]
            for flag in flags:
                command.extend((f"--{flag}", str(getattr(args, flag))))
            if args.command == "emit":
                run_child(command + ["--input", args.input], 0)
            else:
                data = run_child(command, MAX_BUNDLE_BYTES)
                write_private(args.output, data, MAX_BUNDLE_BYTES)
            return 0
        except DeviceError as err:
            print(f"espnow-bundle: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("espnow-bundle: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
