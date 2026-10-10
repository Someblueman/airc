#!/usr/bin/env python3
"""Relay one framed TLS connection through provisioned Linux RDMA endpoints."""

from __future__ import annotations

import argparse
import errno
import ipaddress
import os
import select
import socket
import sys
import time
from contextlib import ExitStack
from typing import TYPE_CHECKING

from gateway_device_io import DeviceError, interruption_handlers, run_child, unblock_child_interrupts

if TYPE_CHECKING:
    from gateway_rdma import RDMALibrary

BUFFER_BYTES = 65536
MAX_DIRECTION_BYTES = 4 * 1024 * 1024
PENDING = (errno.EINPROGRESS, errno.EALREADY, errno.EINTR, errno.EWOULDBLOCK)


def poll_until(lib: RDMALibrary, interests: list[tuple[int, int]], deadline: float) -> dict[int, int]:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise DeviceError("RDMA bridge operation timed out")
    ready = lib.poll(interests, max(0, min(100, int(remaining * 1000))))
    if time.monotonic() >= deadline:
        raise DeviceError("RDMA bridge operation timed out")
    if any(events & (select.POLLERR | select.POLLNVAL) for events in ready.values()):
        raise DeviceError("RDMA bridge endpoint failed")
    return ready


def connected(lib: RDMALibrary, fd: int, rdma: bool, tcp: socket.socket | None, deadline: float) -> None:
    while True:
        events = poll_until(lib, [(fd, select.POLLOUT)], deadline).get(fd, 0)
        if events & select.POLLHUP:
            raise DeviceError("endpoint closed while connecting")
        if events & select.POLLOUT:
            error = lib.socket_error(fd) if rdma else tcp.getsockopt(socket.SOL_SOCKET, socket.SO_ERROR)
            if error:
                raise DeviceError("endpoint connection failed")
            return


def local_socket(stack: ExitStack) -> socket.socket:
    tcp = stack.enter_context(socket.socket(socket.AF_INET, socket.SOCK_STREAM))
    tcp.setblocking(False)
    tcp.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    tcp.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, BUFFER_BYTES // 2)
    tcp.setsockopt(socket.SOL_SOCKET, socket.SO_SNDBUF, BUFFER_BYTES // 2)
    return tcp


def accept_rdma(lib: RDMALibrary, args: argparse.Namespace, stack: ExitStack) -> tuple[int, socket.socket]:
    listener = lib.socket()
    lib.bind(listener, args.local_ip, args.port)
    lib.listen(listener)
    deadline = time.monotonic() + 20
    while True:
        ready = poll_until(lib, [(listener, select.POLLIN)], deadline).get(listener, 0)
        if ready & select.POLLHUP:
            raise DeviceError("RDMA listener closed")
        if not ready & select.POLLIN:
            continue
        accepted = lib.accept(listener)
        if accepted is not None:
            break
    rdma, peer_ip, _peer_port = accepted
    lib.close(listener)
    if peer_ip != args.peer_ip:
        raise DeviceError("RDMA peer does not match the provisioned endpoint")
    tcp = local_socket(stack)
    status = tcp.connect_ex(("127.0.0.1", args.tcp_port))
    if status not in (0, *PENDING):
        raise DeviceError("cannot connect to the fixed local TLS gateway")
    if status:
        connected(lib, tcp.fileno(), False, tcp, time.monotonic() + 5)
    return rdma, tcp


def connect_rdma(lib: RDMALibrary, args: argparse.Namespace, stack: ExitStack) -> tuple[int, socket.socket]:
    listener = local_socket(stack)
    listener.bind(("127.0.0.1", args.tcp_port))
    listener.listen(1)
    deadline = time.monotonic() + 20
    while True:
        ready = poll_until(lib, [(listener.fileno(), select.POLLIN)], deadline).get(listener.fileno(), 0)
        if ready & select.POLLHUP:
            raise DeviceError("local listener closed")
        if not ready & select.POLLIN:
            continue
        try:
            tcp, peer = listener.accept()
        except (BlockingIOError, InterruptedError):
            continue
        stack.enter_context(tcp)
        break
    listener.close()
    if peer[0] != "127.0.0.1":
        raise DeviceError("unexpected local client address")
    tcp.setblocking(False)
    tcp.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    rdma = lib.socket()
    lib.bind(rdma, args.local_ip, 0)
    if not lib.connect(rdma, args.peer_ip, args.port):
        connected(lib, rdma, True, None, time.monotonic() + 5)
    lib.verify_connected(rdma)
    return rdma, tcp


def relay(lib: RDMALibrary, rdma: int, tcp: socket.socket) -> None:
    deadline = time.monotonic() + 30
    fds = (rdma, tcp.fileno())
    pending = [bytearray(), bytearray()]  # Data read from each endpoint, awaiting the other.
    totals = [0, 0]
    ended = [False, False]
    shutdown = [False, False]
    while True:
        if time.monotonic() >= deadline:
            raise DeviceError("RDMA relay timed out")
        if all(ended) and not any(pending):
            if not all(totals):
                raise DeviceError("connection closed without bidirectional data")
            return
        for source in (0, 1):
            if ended[source] and not pending[source] and not shutdown[source]:
                if source == 0:
                    tcp.shutdown(socket.SHUT_WR)
                else:
                    lib.shutdown_write(rdma)
                shutdown[source] = True
        interests = []
        for endpoint in (0, 1):
            events = 0
            if not ended[endpoint] and len(pending[endpoint]) < BUFFER_BYTES:
                events |= select.POLLIN
            if pending[1 - endpoint]:
                events |= select.POLLOUT
            if events:
                interests.append((fds[endpoint], events))
        ready = poll_until(lib, interests, deadline)
        for endpoint in (0, 1):
            events = ready.get(fds[endpoint], 0)
            outgoing = pending[1 - endpoint]
            if outgoing and events & select.POLLOUT:
                try:
                    count = lib.send(rdma, bytes(outgoing)) if endpoint == 0 else tcp.send(outgoing)
                except (BlockingIOError, InterruptedError):
                    count = None
                if count is not None:
                    if count <= 0 or count > len(outgoing):
                        raise DeviceError("relay write made invalid progress")
                    del outgoing[:count]
            incoming = pending[endpoint]
            if not ended[endpoint] and len(incoming) < BUFFER_BYTES and events & (select.POLLIN | select.POLLHUP):
                capacity = min(BUFFER_BYTES - len(incoming), MAX_DIRECTION_BYTES - totals[endpoint] + 1)
                try:
                    data = lib.recv(rdma, capacity) if endpoint == 0 else tcp.recv(capacity)
                except (BlockingIOError, InterruptedError):
                    data = None
                if data is None:
                    continue
                if not data:
                    ended[endpoint] = True
                    continue
                totals[endpoint] += len(data)
                if totals[endpoint] > MAX_DIRECTION_BYTES:
                    raise DeviceError("relay direction exceeded four MiB")
                incoming.extend(data)


def child(args: argparse.Namespace) -> None:
    unblock_child_interrupts()
    import resource
    from gateway_rdma import RDMALibrary

    for kind, bound in ((resource.RLIMIT_AS, 512 * 1024 * 1024), (resource.RLIMIT_NOFILE, 64)):
        soft, hard = resource.getrlimit(kind)
        limit = min(value for value in (bound, soft, hard) if value != resource.RLIM_INFINITY)
        resource.setrlimit(kind, (limit, limit))
    with RDMALibrary(args.library, args.sha256) as lib, ExitStack() as stack:
        if args.command == "_accept-child":
            rdma, tcp = accept_rdma(lib, args, stack)
        else:
            rdma, tcp = connect_rdma(lib, args, stack)
        relay(lib, rdma, tcp)


def validate(args: argparse.Namespace) -> None:
    for value in (args.local_ip, args.peer_ip):
        address = ipaddress.IPv4Address(value)
        if str(address) != value or address.is_unspecified or address.is_multicast or address.is_loopback or int(address) == 0xFFFFFFFF:
            raise DeviceError("RDMA addresses must be explicit non-loopback unicast IPv4 addresses")
    if args.local_ip == args.peer_ip:
        raise DeviceError("RDMA local and peer addresses must differ")
    if not 1024 <= args.port <= 65535 or not 1024 <= args.tcp_port <= 65535:
        raise DeviceError("RDMA and local TCP ports must be in 1024..65535")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    flags = ("local-ip", "peer-ip", "port", "tcp-port", "library", "sha256")
    for name in ("accept", "connect", "_accept-child", "_connect-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else name)
        for flag in flags:
            command.add_argument(f"--{flag}", required=True, type=int if flag in ("port", "tcp-port") else str)
    args = parser.parse_args(argv)
    with interruption_handlers():
        try:
            validate(args)
            if args.command.startswith("_"):
                child(args)
                return 0
            command = [sys.executable, os.path.abspath(__file__), f"_{args.command}-child"]
            for flag in flags:
                command.extend((f"--{flag}", str(getattr(args, flag.replace("-", "_")))))
            run_child(command, 0, timeout=65)
            return 0
        except DeviceError as err:
            print(f"rdma-bridge: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("rdma-bridge: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
