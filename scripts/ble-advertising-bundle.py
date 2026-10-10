#!/usr/bin/env python3
"""Carry a signed bundle in BLE advertisements on a dedicated controller."""

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
# Each private service-data UUID identifies one exact wire format.
SERVICE_UUID = uuid.UUID("95a73d2b-a997-451a-9378-2a79c08b1b01")
SERVICE_WIRE = SERVICE_UUID.bytes[::-1]
EXTENDED_WIRE = uuid.UUID("95a73d2b-a997-451a-9378-2a79c08b1b02").bytes[::-1]
EXTENDED_CHUNK_BYTES = 180
ADDRESS = re.compile(r"(?:[0-9A-F]{2}:){5}[0-9A-F]{2}")


def address(value: str) -> bytes:
    if ADDRESS.fullmatch(value) is None:
        raise DeviceError("controller and peer addresses must be uppercase public Bluetooth addresses")
    result = bytes.fromhex(value.replace(":", ""))
    if result in (b"\0" * 6, b"\xff" * 6):
        raise DeviceError("zero and all-ones Bluetooth addresses are unsupported")
    return result


def emit_bundle(controller: HCIController, bundle: bytes, extended: bool = False) -> None:
    if not 1 <= len(bundle) <= MAX_BUNDLE_BYTES:
        raise DeviceError("BLE advertising bundle must contain 1..2048 bytes")
    stream = hashlib.sha256(bundle).digest() + bundle
    chunk_bytes = EXTENDED_CHUNK_BYTES if extended else CHUNK_BYTES
    service = EXTENDED_WIRE if extended else SERVICE_WIRE
    controller.setup_advertise()
    # Repetition gives a passive receiver a second opportunity for lost records.
    # No acknowledgement or claimed reception follows from either sweep.
    for _ in range(2):
        for index, start in enumerate(range(0, len(stream), chunk_bytes)):
            record = struct.pack(">HH", len(stream), index) + stream[start : start + chunk_bytes].ljust(chunk_bytes, b"\0")
            controller.advertise(bytes((21 + chunk_bytes, 0x21)) + service + record)
            controller.dwell(0.22)


def legacy_records(payload: bytes, peer: bytes) -> Iterator[bytes]:
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


def extended_records(payload: bytes, peer: bytes) -> Iterator[bytes]:
    if len(payload) < 2 or payload[0] != 0x0D or not 1 <= payload[1] <= 10:
        raise DeviceError("unexpected extended LE advertising report")
    offset = 2
    found = []
    for _ in range(payload[1]):
        if len(payload) - offset < 24:
            raise DeviceError("truncated extended LE advertising report header")
        event_type = int.from_bytes(payload[offset : offset + 2], "little")
        address_type = payload[offset + 2]
        source = payload[offset + 3 : offset + 9][::-1]
        primary_phy, secondary_phy, sid = payload[offset + 9 : offset + 12]
        periodic_interval = int.from_bytes(payload[offset + 14 : offset + 16], "little")
        size = payload[offset + 23]
        end = offset + 24 + size
        if (
            event_type & ~0x7F
            or (event_type & 0x60) == 0x60
            or address_type not in (0, 1, 2, 3, 0xFF)
            or primary_phy not in (1, 3)
            or secondary_phy not in (0, 1, 2, 3)
            or (sid > 15 and sid != 0xFF)
            or end > len(payload)
        ):
            raise DeviceError("invalid extended LE advertising report fields")
        data = payload[offset + 24 : end]
        offset = end
        if address_type != 0 or source != peer:
            continue
        if len(data) < 18 or data[1] != 0x21 or data[2:18] != EXTENDED_WIRE:
            continue
        # Directed-address fields have no meaning for our undirected packets.
        # Explicitly require complete extended data, never legacy or scan replies.
        if event_type != 0 or (primary_phy, secondary_phy, sid, periodic_interval) != (1, 1, 0, 0):
            raise DeviceError("private BLE data has the wrong extended advertising mode")
        if len(data) != 202 or data[0] != 201:
            raise DeviceError("private extended BLE service data has an invalid length")
        found.append(data[18:])
    if offset != len(payload):
        raise DeviceError("trailing bytes after extended LE advertising reports")
    yield from found


def read_bundle(controller: HCIController, peer: bytes, extended: bool = False) -> bytes:
    controller.setup_scan()
    chunk_bytes = EXTENDED_CHUNK_BYTES if extended else CHUNK_BYTES
    records = extended_records if extended else legacy_records
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
            count = (size + chunk_bytes - 1) // chunk_bytes
            if index >= count:
                raise DeviceError("BLE fragment index is outside its sequence")
            chunk_size = min(chunk_bytes, size - index * chunk_bytes)
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
    extended = args.mode == "extended"
    with HCIController(
        args.controller, address(args.local), timeout=30.0 if extended else TIMEOUT, extended=extended
    ) as controller:
        if bundle is not None:
            emit_bundle(controller, bundle, extended)
            return
        data = read_bundle(controller, address(args.peer), extended)
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
        command.add_argument("--mode", choices=("legacy", "extended"), default="legacy")
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
                "--controller", str(args.controller), "--local", args.local, "--mode", args.mode,
            ]
            timeout = 30.0 if args.mode == "extended" else TIMEOUT
            if args.command == "emit":
                run_child(command + ["--input", args.input], 0, timeout=timeout)
            else:
                data = run_child(command + ["--peer", args.peer], MAX_BUNDLE_BYTES, timeout=timeout)
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
