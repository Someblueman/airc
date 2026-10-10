#!/usr/bin/env python3
"""Carry a signed bundle in legacy BLE advertisements on a dedicated controller."""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import struct
import sys
import uuid
from typing import TYPE_CHECKING, Iterator

from gateway_device_io import (
    DeviceError,
    interruption_handlers,
    read_regular,
    run_child,
    unblock_child_interrupts,
    write_private,
)

if TYPE_CHECKING:
    from gateway_hci import HCIController

MAX_BUNDLE_BYTES = 2048
CHUNK_BYTES = 9
TIMEOUT = 120.0
# Private service-data namespace; UUID version identifies this exact wire format.
SERVICE_UUID = uuid.UUID("95a73d2b-a997-451a-9378-2a79c08b1b01")
SERVICE_WIRE = SERVICE_UUID.bytes[::-1]
ADDRESS = re.compile(r"(?:[0-9A-F]{2}:){5}[0-9A-F]{2}")


def address(value: str) -> bytes:
    if ADDRESS.fullmatch(value) is None:
        raise DeviceError("controller and peer addresses must be uppercase public Bluetooth addresses")
    result = bytes.fromhex(value.replace(":", ""))
    if result in (b"\0" * 6, b"\xff" * 6):
        raise DeviceError("zero and all-ones Bluetooth addresses are unsupported")
    return result


def emit_bundle(controller: HCIController, bundle: bytes) -> None:
    if not 1 <= len(bundle) <= MAX_BUNDLE_BYTES:
        raise DeviceError("BLE advertising bundle must contain 1..2048 bytes")
    stream = hashlib.sha256(bundle).digest() + bundle
    controller.setup_advertise()
    # Repetition gives a passive receiver a second opportunity for lost records.
    # No acknowledgement or claimed reception follows from either sweep.
    for _ in range(2):
        for index, start in enumerate(range(0, len(stream), CHUNK_BYTES)):
            record = struct.pack(">HH", len(stream), index) + stream[start : start + CHUNK_BYTES].ljust(CHUNK_BYTES, b"\0")
            controller.advertise(b"\x1e\x21" + SERVICE_WIRE + record)
            controller.dwell(0.22)


def records(payload: bytes, peer: bytes) -> Iterator[bytes]:
    if len(payload) < 2 or payload[0] != 0x02 or not 1 <= payload[1] <= 25:
        raise DeviceError("unexpected legacy LE advertising report")
    offset = 2
    found = []
    for _ in range(payload[1]):
        if len(payload) - offset < 10:
            raise DeviceError("truncated LE advertising report header")
        event_type, address_type = payload[offset : offset + 2]
        source = payload[offset + 2 : offset + 8][::-1]
        size = payload[offset + 8]
        end = offset + 9 + size
        if event_type > 4 or address_type > 3 or size > 31 or end >= len(payload):
            raise DeviceError("invalid legacy LE advertising report fields")
        data = payload[offset + 9 : end]
        offset = end + 1  # Skip RSSI; it grants no authority.
        if event_type != 3 or address_type != 0 or source != peer:
            continue
        if len(data) < 18 or data[1] != 0x21 or data[2:18] != SERVICE_WIRE:
            continue
        if len(data) != 31 or data[0] != 30:
            raise DeviceError("private BLE service data has an invalid length")
        found.append(data[18:])
    if offset != len(payload):
        raise DeviceError("trailing bytes after LE advertising reports")
    yield from found


def read_bundle(controller: HCIController, peer: bytes) -> bytes:
    controller.setup_scan()
    total = 0
    slots: list[bytes | None] = []
    received = 0
    while True:
        event, payload = controller.event()
        if event != 0x3E:
            raise DeviceError("unexpected controller event during BLE scan")
        for record in records(payload, peer):
            size, index = struct.unpack(">HH", record[:4])
            if not 33 <= size <= MAX_BUNDLE_BYTES + 32:
                raise DeviceError("BLE sequence size is outside its bound")
            count = (size + CHUNK_BYTES - 1) // CHUNK_BYTES
            if index >= count:
                raise DeviceError("BLE fragment index is outside its sequence")
            chunk_size = min(CHUNK_BYTES, size - index * CHUNK_BYTES)
            if any(record[4 + chunk_size :]):
                raise DeviceError("BLE final-fragment padding must be zero")
            if total == 0:
                total = size
                slots = [None] * count
            if size != total:
                raise DeviceError("mixed BLE sequence lengths")
            chunk = record[4 : 4 + chunk_size]
            if slots[index] is not None:
                if slots[index] != chunk:
                    raise DeviceError("conflicting duplicate BLE fragment")
                continue
            slots[index] = chunk
            received += 1
            if received == len(slots):
                stream = b"".join(part for part in slots if part is not None)
                if len(stream) != total or hashlib.sha256(stream[32:]).digest() != stream[:32]:
                    raise DeviceError("BLE signed-bundle integrity check failed")
                return stream[32:]


def child(args: argparse.Namespace) -> None:
    unblock_child_interrupts()
    from gateway_hci import HCIController

    bundle = read_regular(args.input, MAX_BUNDLE_BYTES) if args.command == "_emit-child" else None
    with HCIController(args.controller, address(args.local), timeout=TIMEOUT) as controller:
        if bundle is not None:
            emit_bundle(controller, bundle)
            return
        data = read_bundle(controller, address(args.peer))
    offset = 0
    while offset < len(data):
        count = os.write(1, data[offset:])
        if count <= 0:
            raise DeviceError("BLE child output made no progress")
        offset += count


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("read", "emit", "_read-child", "_emit-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else name)
        command.add_argument("--controller", required=True, type=int)
        command.add_argument("--local", required=True)
        if name in ("emit", "_emit-child"):
            command.add_argument("--input", required=True)
        else:
            command.add_argument("--peer", required=True)
        if name == "read":
            command.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    with interruption_handlers():
        try:
            if not 0 <= args.controller <= 65534:
                raise DeviceError("controller index must be 0..65534")
            local = address(args.local)
            if args.command in ("read", "_read-child") and address(args.peer) == local:
                raise DeviceError("local and peer addresses must differ")
            if args.command.startswith("_"):
                child(args)
                return 0
            command = [
                sys.executable, os.path.abspath(__file__), f"_{args.command}-child",
                "--controller", str(args.controller), "--local", args.local,
            ]
            if args.command == "emit":
                run_child(command + ["--input", args.input], 0, timeout=TIMEOUT)
            else:
                data = run_child(command + ["--peer", args.peer], MAX_BUNDLE_BYTES, timeout=TIMEOUT)
                write_private(args.output, data, MAX_BUNDLE_BYTES)
            return 0
        except DeviceError as err:
            print(f"ble-advertising-bundle: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("ble-advertising-bundle: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
