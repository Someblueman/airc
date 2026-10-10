#!/usr/bin/env python3
"""Carry one signed bundle over provisioned Digi XBee 3 Zigbee API2 radios."""

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
    from xbee_api import XBeeLink

MAX_BUNDLE_BYTES = 2048
RECORD_BYTES = 64
CHUNK_BYTES = 24
ADDRESS = re.compile(r"[0-9A-F]{16}")
APPLICATION = b"\xe8\xe8\x00\x11\xc1\x05"


def address(value: str) -> bytes:
    if ADDRESS.fullmatch(value) is None or value in ("0000000000000000", "000000000000FFFF", "FFFFFFFFFFFFFFFF"):
        raise DeviceError("radio address must be an exact uppercase unicast EUI64")
    return bytes.fromhex(value)


def query(link: XBeeLink, command: bytes) -> bytes:
    # Command-only local AT request: no parameter write, apply or save command.
    frame_id = link.next_frame_id()
    link.send_frame(bytes((0x08, frame_id)) + command)
    response = link.read_frame()
    if len(response) < 5 or response[:4] != bytes((0x88, frame_id)) + command or response[4] != 0:
        raise DeviceError("radio returned an unexpected or failed local AT response")
    return response[5:]


def prepare(link: XBeeLink, local: bytes) -> None:
    high, low = query(link, b"SH"), query(link, b"SL")
    if len(high) != 4 or len(low) != 4 or high + low != local:
        raise DeviceError("local radio identity does not match provisioning")
    for command, expected in ((b"AP", 2), (b"AO", 1), (b"EE", 1), (b"AI", 0), (b"D6", 0), (b"D7", 0), (b"SM", 0)):
        if query(link, command) != bytes([expected]):
            raise DeviceError("radio API, encryption, association, sleep or flow-control configuration is unsupported")
    capacity = query(link, b"NP")
    if not 1 <= len(capacity) <= 2 or int.from_bytes(capacity, "big") < RECORD_BYTES:
        raise DeviceError("radio API payload capacity is too small")


def emit_bundle(link: XBeeLink, peer: bytes, bundle: bytes) -> None:
    if not 1 <= len(bundle) <= MAX_BUNDLE_BYTES:
        raise DeviceError("Zigbee bundle must contain 1..2048 bytes")
    digest = hashlib.sha256(bundle).digest()
    for index, start in enumerate(range(0, len(bundle), CHUNK_BYTES)):
        chunk = bundle[start : start + CHUNK_BYTES]
        record = b"AZB1" + len(bundle).to_bytes(2, "big") + index.to_bytes(2, "big") + digest + chunk.ljust(CHUNK_BYTES, b"\0")
        # Explicit unicast, unknown 16-bit address, radius/default options zero.
        frame_id = link.next_frame_id()
        link.send_frame(bytes((0x11, frame_id)) + peer + b"\xff\xfe" + APPLICATION + b"\0\0" + record)
        response = link.read_frame()
        if len(response) != 7 or response[:2] != bytes((0x8B, frame_id)) or response[5] != 0:
            raise DeviceError("radio did not confirm the current unicast packet")


def receive_record(link: XBeeLink, peer: bytes) -> bytes:
    response = link.read_frame()
    if (
        len(response) != 18 + RECORD_BYTES
        or response[0] != 0x91
        or response[1:9] != peer
        or response[11:17] != APPLICATION
        # Bit 0 has firmware-specific ACK/retry meaning; no authority is inferred.
        # This fixed profile does not enable APS or secure-session options.
        or response[17] & 0xFE
    ):
        raise DeviceError("radio packet source, application address, mode or length is unexpected")
    return response[18:]


def read_bundle(link: XBeeLink, peer: bytes) -> bytes:
    first = receive_record(link, peer)
    total = int.from_bytes(first[4:6], "big")
    if first[:4] != b"AZB1" or not 1 <= total <= MAX_BUNDLE_BYTES or first[6:8] != b"\0\0":
        raise DeviceError("Zigbee sequence must start at the first bounded AZB1 record")
    digest = first[8:40]
    count = (total + CHUNK_BYTES - 1) // CHUNK_BYTES
    bundle = bytearray()
    for index in range(count):
        record = first if index == 0 else receive_record(link, peer)
        if record[:6] != first[:6] or record[8:40] != digest or int.from_bytes(record[6:8], "big") != index:
            raise DeviceError("Zigbee sequence is mixed, duplicated, missing or out of order")
        size = min(CHUNK_BYTES, total - len(bundle))
        if any(record[40 + size :]):
            raise DeviceError("Zigbee final-record padding must be zero")
        bundle.extend(record[40 : 40 + size])
    if len(bundle) != total or hashlib.sha256(bundle).digest() != digest:
        raise DeviceError("Zigbee bundle integrity check failed")
    return bytes(bundle)


def child(args: argparse.Namespace) -> None:
    unblock_child_interrupts()
    from xbee_api import XBeeLink

    local, peer = address(args.local), address(args.peer)
    if local == peer:
        raise DeviceError("local and peer radio addresses must differ")
    with XBeeLink(args.device) as link:
        prepare(link, local)
        if args.command == "_emit-child":
            emit_bundle(link, peer, read_regular(args.input, MAX_BUNDLE_BYTES))
            return
        data = read_bundle(link, peer)
    offset = 0
    while offset < len(data):
        count = os.write(1, data[offset:])
        if count <= 0:
            raise DeviceError("Zigbee child output made no progress")
        offset += count


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("read", "emit", "_read-child", "_emit-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else name)
        for flag in ("device", "local", "peer"):
            command.add_argument(f"--{flag}", required=True)
        if name in ("emit", "_emit-child"):
            command.add_argument("--input", required=True)
        elif name == "read":
            command.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    with interruption_handlers():
        try:
            if args.command.startswith("_"):
                child(args)
                return 0
            command = [
                sys.executable, os.path.abspath(__file__), f"_{args.command}-child",
                "--device", args.device, "--local", args.local, "--peer", args.peer,
            ]
            if args.command == "emit":
                run_child(command + ["--input", args.input], 0)
            else:
                data = run_child(command, MAX_BUNDLE_BYTES)
                write_private(args.output, data, MAX_BUNDLE_BYTES)
            return 0
        except DeviceError as err:
            print(f"zigbee-bundle: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("zigbee-bundle: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
