#!/usr/bin/env python3
"""Carry one signed bundle over fixed vendor USB interrupt input reports."""

from __future__ import annotations

import argparse
import os
import sys

from gateway_device_io import (
    DeviceError,
    interruption_handlers,
    read_regular,
    run_child,
    unblock_child_interrupts,
    write_private,
)

MAX_BUNDLE_BYTES = 16384


def _child(args: argparse.Namespace) -> None:
    unblock_child_interrupts()
    # Linux-only ioctl/device code is loaded inside the supervised child.
    from usb_hid_transport import emit_bundle, read_bundle

    if args.command == "_emit-child":
        emit_bundle(read_regular(args.input, MAX_BUNDLE_BYTES), args.function)
        return
    data = read_bundle(args.device)
    if not 1 <= len(data) <= MAX_BUNDLE_BYTES:
        raise DeviceError("USB reader returned an invalid bundle size")
    offset = 0
    while offset < len(data):
        written = os.write(1, data[offset:])
        if written <= 0:
            raise DeviceError("USB reader output made no progress")
        offset += written


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("read", "_read-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else "receive one bundle")
        command.add_argument("--device", required=True)
        if name == "read":
            command.add_argument("--output", required=True)
    for name in ("emit", "_emit-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else "send one bundle")
        command.add_argument("--input", required=True)
        command.add_argument("--function", required=True)
    args = parser.parse_args(argv)
    with interruption_handlers():
        try:
            child = [sys.executable, os.path.abspath(__file__)]
            if args.command == "read":
                data = run_child(child + ["_read-child", "--device", args.device], MAX_BUNDLE_BYTES)
                write_private(args.output, data, MAX_BUNDLE_BYTES)
            elif args.command == "emit":
                run_child(child + ["_emit-child", "--input", args.input, "--function", args.function], 0)
            else:
                _child(args)
            return 0
        except DeviceError as err:
            print(f"usb-hid-bundle: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("usb-hid-bundle: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
