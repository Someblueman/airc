#!/usr/bin/env python3
"""Send or receive one signed AIRC bundle through a selected ALSA audio modem."""

from __future__ import annotations

import argparse
import base64
import binascii
import hashlib
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

MAX_BUNDLE_BYTES = 2048
MAX_FRAME_BYTES = 3000
FRAME = re.compile(rb"AIRCA1:([1-9][0-9]{0,3}):([0-9a-f]{64}):([A-Za-z0-9+/]+={0,2})\n")


def encode_frame(bundle: bytes) -> bytes:
    if not 1 <= len(bundle) <= MAX_BUNDLE_BYTES:
        raise DeviceError("acoustic bundle must contain 1..2048 bytes")
    digest = hashlib.sha256(bundle).hexdigest().encode("ascii")
    return b"AIRCA1:" + str(len(bundle)).encode("ascii") + b":" + digest + b":" + base64.b64encode(bundle) + b"\n"


def decode_frame(frame: bytes) -> bytes:
    if not 1 <= len(frame) <= MAX_FRAME_BYTES:
        raise DeviceError("acoustic frame is outside its size bound")
    match = FRAME.fullmatch(frame)
    if match is None:
        raise DeviceError("acoustic frame is incomplete or malformed")
    declared, digest, encoded = match.groups()
    if not 1 <= int(declared) <= MAX_BUNDLE_BYTES:
        raise DeviceError("acoustic bundle length is outside its size bound")
    try:
        bundle = base64.b64decode(encoded, validate=True)
    except binascii.Error as err:
        raise DeviceError("acoustic payload is not canonical base64") from err
    if base64.b64encode(bundle) != encoded or len(bundle) != int(declared):
        raise DeviceError("acoustic payload length or base64 is invalid")
    if hashlib.sha256(bundle).hexdigest().encode("ascii") != digest:
        raise DeviceError("acoustic bundle digest does not match")
    return bundle


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("read", "emit", "_read-child", "_emit-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else name)
        command.add_argument("--modem", required=True)
        command.add_argument("--sha256", required=True)
        command.add_argument("--device", required=True)
        if name in ("emit", "_emit-child"):
            command.add_argument("--input", required=True)
        elif name == "read":
            command.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    with interruption_handlers():
        try:
            require_linux()
            if args.command.startswith("_"):
                unblock_child_interrupts()
                from acoustic_transport import exec_modem

                frame = encode_frame(read_regular(args.input, MAX_BUNDLE_BYTES)) if args.command == "_emit-child" else None
                exec_modem(args.modem, args.sha256, args.device, "tx" if frame is not None else "rx", frame)
            command = [
                sys.executable, os.path.abspath(__file__), f"_{args.command}-child",
                "--modem", args.modem, "--sha256", args.sha256, "--device", args.device,
            ]
            if args.command == "emit":
                command.extend(["--input", args.input])
                run_child(command, 0)
            else:
                frame = run_child(command, MAX_FRAME_BYTES)
                write_private(args.output, decode_frame(frame), MAX_BUNDLE_BYTES)
            return 0
        except DeviceError as err:
            print(f"acoustic-bundle: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("acoustic-bundle: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
