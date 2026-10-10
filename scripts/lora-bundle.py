#!/usr/bin/env python3
"""Carry one signed bundle through provisioned REYAX RYLR998 LoRa radios."""

from __future__ import annotations

import argparse
import base64
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

MAX_BUNDLE_BYTES = 2048
CHUNK_BYTES = 96
RECORD = re.compile(rb"ALR1:([0-9A-F]{4}):([0-9A-F]{2}):([0-9a-f]{64}):([A-Za-z0-9+/]+={0,2})")
NUMBER = re.compile(rb"[0-9]{1,9}")
SIGNAL = re.compile(rb"-?[0-9]{1,3}")


def read_line(link: SerialLink) -> bytes:
    link.verify_path()
    # Some firmware emits a separating CRLF. Never skip an unbounded stream.
    for _ in range(3):
        line = bytearray()
        for _ in range(320):
            value = link.read_byte()
            if value == 13:
                if link.read_byte() != 10:
                    raise DeviceError("radio line is not CRLF terminated")
                break
            if not 32 <= value <= 126:
                raise DeviceError("radio line contains an unexpected byte")
            line.append(value)
        else:
            raise DeviceError("radio line exceeds its bound")
        if line:
            link.verify_path()
            return bytes(line)
    raise DeviceError("too many empty radio lines")


def query(link: SerialLink, command: bytes, prefix: bytes) -> bytes:
    link.write(b"AT+" + command + b"?\r\n")
    response = read_line(link)
    if not response.startswith(prefix):
        raise DeviceError("radio returned an unexpected query response")
    return response[len(prefix) :]


def prepare(link: SerialLink, args: argparse.Namespace) -> None:
    if query(link, b"UID", b"+UID=").upper() != args.uid.encode("ascii"):
        raise DeviceError("local radio UID does not match provisioning")
    if query(link, b"VER", b"+VER=") != args.firmware.encode("ascii"):
        raise DeviceError("local radio firmware does not match provisioning")
    for command, prefix, expected in (
        (b"IPR", b"+IPR=", 115200),
        (b"MODE", b"+MODE=", 0),
        (b"ADDRESS", b"+ADDRESS=", args.local),
        # The documented reply is NETWORK, although the query is NETWORKID.
        (b"NETWORKID", b"+NETWORK=", args.network),
        (b"BAND", b"+BAND=", args.band),
        (b"CRFOP", b"+CRFOP=", args.power),
    ):
        value = query(link, command, prefix)
        if NUMBER.fullmatch(value) is None or int(value) != expected:
            raise DeviceError("radio configuration does not match the selected profile")
    if query(link, b"PARAMETER", b"+PARAMETER=") != b"8,7,1,12":
        raise DeviceError("radio RF parameters must be 8,7,1,12")


def emit_bundle(link: SerialLink, peer: int, bundle: bytes) -> None:
    if not 1 <= len(bundle) <= MAX_BUNDLE_BYTES:
        raise DeviceError("LoRa bundle must contain 1..2048 bytes")
    digest = hashlib.sha256(bundle).hexdigest()
    for index, start in enumerate(range(0, len(bundle), CHUNK_BYTES)):
        record = f"ALR1:{len(bundle):04X}:{index:02X}:{digest}:".encode("ascii")
        record += base64.b64encode(bundle[start : start + CHUNK_BYTES])
        link.write(f"AT+SEND={peer},{len(record)},".encode("ascii") + record + b"\r\n")
        if read_line(link) != b"+OK":
            raise DeviceError("radio did not complete the current send command")


def receive_record(link: SerialLink, peer: int) -> tuple[int, int, bytes, bytes]:
    line = read_line(link)
    if not line.startswith(b"+RCV="):
        raise DeviceError("expected a radio receive record")
    fields = [field.strip(b" ") for field in line[5:].split(b",")]
    if (
        len(fields) != 5
        or NUMBER.fullmatch(fields[0]) is None
        or int(fields[0]) != peer
        or NUMBER.fullmatch(fields[1]) is None
        or int(fields[1]) != len(fields[2])
        or not 1 <= len(fields[2]) <= 206
        or SIGNAL.fullmatch(fields[3]) is None
        or SIGNAL.fullmatch(fields[4]) is None
    ):
        raise DeviceError("radio source, length or receive metadata is unexpected")
    match = RECORD.fullmatch(fields[2])
    if match is None:
        raise DeviceError("malformed ALR1 application record")
    chunk = base64.b64decode(match[4], validate=True)
    if not 1 <= len(chunk) <= CHUNK_BYTES or base64.b64encode(chunk) != match[4]:
        raise DeviceError("radio payload encoding is not canonical")
    return int(match[1], 16), int(match[2], 16), match[3], chunk


def read_bundle(link: SerialLink, peer: int) -> bytes:
    first = receive_record(link, peer)
    total, index, digest, _ = first
    if not 1 <= total <= MAX_BUNDLE_BYTES or index != 0:
        raise DeviceError("LoRa sequence must start at its first bounded record")
    bundle = bytearray()
    for index in range((total + CHUNK_BYTES - 1) // CHUNK_BYTES):
        record = first if index == 0 else receive_record(link, peer)
        expected_size = min(CHUNK_BYTES, total - len(bundle))
        if record[:3] != (total, index, digest) or len(record[3]) != expected_size:
            raise DeviceError("LoRa sequence is mixed, duplicated, missing or out of order")
        bundle.extend(record[3])
    if len(bundle) != total or hashlib.sha256(bundle).hexdigest().encode("ascii") != digest:
        raise DeviceError("LoRa bundle integrity check failed")
    return bytes(bundle)


def child(args: argparse.Namespace) -> None:
    unblock_child_interrupts()
    from gateway_serial import SerialLink

    with SerialLink(args.device) as link:
        prepare(link, args)
        if args.command == "_emit-child":
            emit_bundle(link, args.peer, read_regular(args.input, MAX_BUNDLE_BYTES))
            return
        data = read_bundle(link, args.peer)
    offset = 0
    while offset < len(data):
        count = os.write(1, data[offset:])
        if count <= 0:
            raise DeviceError("LoRa child output made no progress")
        offset += count


def validate(args: argparse.Namespace) -> None:
    if not 1 <= args.local <= 65535 or not 1 <= args.peer <= 65535 or args.local == args.peer:
        raise DeviceError("local and peer must be distinct unicast addresses in 1..65535")
    if args.network not in (*range(3, 16), 18):
        raise DeviceError("network must be 3..15 or 18")
    if not 820000000 <= args.band <= 960000000 or not 0 <= args.power <= 22:
        raise DeviceError("band or power is outside the supported radio range")
    if re.fullmatch(r"[0-9A-F]{24}", args.uid) is None:
        raise DeviceError("UID must contain exactly 24 uppercase hexadecimal digits")
    if re.fullmatch(r"RYLR998_V[A-Za-z0-9.]{1,32}", args.firmware) is None:
        raise DeviceError("firmware must name the exact provisioned RYLR998 version")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    flags = ("device", "uid", "firmware", "local", "peer", "network", "band", "power")
    for name in ("read", "emit", "_read-child", "_emit-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else name)
        for flag in flags:
            command.add_argument(f"--{flag}", required=True, type=int if flag in flags[3:] else str)
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
            print(f"lora-bundle: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("lora-bundle: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
