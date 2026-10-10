#!/usr/bin/env python3
"""Transfer one signed AIRC bundle through the fixed Cyclone DDS profile."""

from __future__ import annotations

import argparse
import ipaddress
import os
import re
import signal
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
from gateway_native_io import install_stdin, open_native

MAX_BUNDLE = 32768


def validate_options(domain: str, local: str, peer: str) -> None:
    if re.fullmatch(r"0|[1-9][0-9]{0,2}", domain, flags=re.ASCII) is None or int(domain) > 230:
        raise DeviceError("DDS domain must be a canonical number from 0 to 230")
    for address in (local, peer):
        ip = ipaddress.IPv4Address(address)
        if str(ip) != address or ip.is_unspecified or ip.is_multicast or address == "255.255.255.255":
            raise DeviceError("DDS addresses must be canonical unicast IPv4 addresses")
    if local == peer:
        raise DeviceError("DDS local and peer addresses must be distinct")


def execute(args: argparse.Namespace) -> None:
    """Replace the supervised child; native code owns its DDS threads."""
    unblock_child_interrupts()
    signal.signal(signal.SIGALRM, signal.SIG_DFL)
    signal.alarm(30)
    data = read_regular(args.input, MAX_BUNDLE) if args.command == "_emit-child" else None
    descriptor = open_native(args.helper, args.sha256)
    try:
        install_stdin(data, MAX_BUNDLE)
        mode = "emit" if data is not None else "read"
        # No inherited DDS URI, loader overrides, plugin paths or shell expansion.
        os.execve(descriptor, ["airc-dds", mode, args.domain, args.local, args.peer], {"LANG": "C"})
    finally:
        os.close(descriptor)
    raise DeviceError("DDS helper exec returned unexpectedly")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("read", "emit", "_read-child", "_emit-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else name)
        for option in ("helper", "sha256", "domain", "local", "peer"):
            command.add_argument(f"--{option}", required=True)
        if name in ("emit", "_emit-child"):
            command.add_argument("--input", required=True)
        elif name == "read":
            command.add_argument("--output", required=True)
    args = parser.parse_args()
    with interruption_handlers():
        try:
            require_linux()
            validate_options(args.domain, args.local, args.peer)
            if args.command.startswith("_"):
                execute(args)
            command = [sys.executable, os.path.abspath(__file__), f"_{args.command}-child"]
            for option in ("helper", "sha256", "domain", "local", "peer"):
                command.extend([f"--{option}", getattr(args, option)])
            if args.command == "emit":
                command.extend(["--input", args.input])
                run_child(command, 0)
            else:
                data = run_child(command, MAX_BUNDLE)
                write_private(args.output, data, MAX_BUNDLE)
            return 0
        except DeviceError as err:
            print(f"dds-bundle: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("dds-bundle: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
