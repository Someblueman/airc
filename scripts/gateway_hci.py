"""Small bounded HCI user-channel owner for the BLE advertising profile."""

from __future__ import annotations

import ctypes
import math
import os
import selectors
import signal
import socket
import time
from types import TracebackType

from gateway_device_io import DeviceError, INTERRUPT_SIGNALS, require_linux

HCI_CHANNEL_USER = 1
MAX_EVENTS = 16_384
MAX_EVENT_BYTES = 258
MAX_COMMAND_PARAMS = 255
MAX_TIMEOUT = 120.0
COMMAND_TIMEOUT = 3.0


class _SockaddrHCI(ctypes.Structure):
    _fields_ = [
        ("family", ctypes.c_ushort),
        ("device", ctypes.c_ushort),
        ("channel", ctypes.c_ushort),
    ]


def _bind_user_channel(sock: socket.socket, index: int) -> None:
    if ctypes.sizeof(_SockaddrHCI) != 6:
        raise DeviceError("unsupported Linux HCI sockaddr layout")
    address = _SockaddrHCI(socket.AF_BLUETOOTH, index, HCI_CHANNEL_USER)
    libc = ctypes.CDLL(None, use_errno=True)
    bind = libc.bind
    bind.argtypes = (ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32)
    bind.restype = ctypes.c_int
    if bind(sock.fileno(), ctypes.byref(address), ctypes.sizeof(address)) != 0:
        error = ctypes.get_errno()
        raise OSError(error, os.strerror(error))


class HCIController:
    """Own one dedicated HCI user channel on an operator-dedicated controller."""

    def __init__(
        self,
        index: int,
        local: bytes,
        timeout: float = MAX_TIMEOUT,
        extended: bool = False,
    ) -> None:
        require_linux()
        if isinstance(index, bool) or not isinstance(index, int) or not 0 <= index <= 65534:
            raise DeviceError("controller index must be 0..65534")
        if not isinstance(local, bytes) or len(local) != 6 or local in (b"\0" * 6, b"\xff" * 6):
            raise DeviceError("local address must be a nonzero six-byte public address")
        if not isinstance(extended, bool):
            raise DeviceError("extended advertising selector must be boolean")
        if (
            isinstance(timeout, bool)
            or not isinstance(timeout, (int, float))
            or not math.isfinite(timeout)
            or not 0 < timeout <= MAX_TIMEOUT
        ):
            raise DeviceError("controller timeout must be in 0..120 seconds")

        self.index = index
        self.local = local
        self.extended = extended
        self.deadline = time.monotonic() + float(timeout)
        self._events = 0
        self._sock: socket.socket | None = None
        self._selector: selectors.BaseSelector | None = None
        self._advertising = False
        self._scanning = False
        self._advertising_configured = False

        old_mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
        try:
            self._sock = socket.socket(socket.AF_BLUETOOTH, socket.SOCK_RAW, socket.BTPROTO_HCI)
            self._sock.setblocking(False)
            _bind_user_channel(self._sock, index)
            self._selector = selectors.DefaultSelector()
            self._selector.register(self._sock, selectors.EVENT_READ)
        except BaseException:
            self._close_descriptors()
            raise
        finally:
            try:
                signal.pthread_sigmask(signal.SIG_SETMASK, old_mask)
            except BaseException:
                self._close_descriptors()
                raise

        try:
            # Read and verify the controller identity before resetting it.
            address = self.command(0x1009, b"")
            if len(address) != 6 or address[::-1] != local:
                raise DeviceError("controller public address does not match the requested local address")
            self._command_empty(0x0C03, b"")
            self._command_empty(0x0C01, bytes.fromhex("00e0000000000020"))
            if extended:
                features = self.command(0x2003, b"")
                if len(features) != 8 or not features[1] & 0x10:
                    raise DeviceError("controller does not support extended advertising")
                maximum = self.command(0x203A, b"")
                if len(maximum) != 2 or int.from_bytes(maximum, "little") < 202:
                    raise DeviceError("controller extended advertising data limit is below 202 bytes")
                sets = self.command(0x203B, b"")
                if len(sets) != 1 or sets[0] < 1:
                    raise DeviceError("controller does not support an advertising set")
                le_mask = (1 << 12).to_bytes(8, "little")
            else:
                le_mask = b"\x02" + b"\0" * 7
            self._command_empty(0x2001, le_mask)
        except BaseException:
            try:
                self.close()
            except BaseException:
                pass
            raise

    def __enter__(self) -> HCIController:
        return self

    def __exit__(
        self,
        _exc_type: type[BaseException] | None,
        _exc: BaseException | None,
        _traceback: TracebackType | None,
    ) -> None:
        if _exc_type is None:
            self.close()
        else:
            try:
                self.close()
            except BaseException:
                pass

    def _remaining(self, deadline: float | None = None, *, cleanup: bool = False) -> float:
        end = deadline if cleanup and deadline is not None else self.deadline
        if deadline is not None and not cleanup:
            end = min(self.deadline, deadline)
        remaining = end - time.monotonic()
        if remaining <= 0:
            raise DeviceError("HCI operation timed out")
        return remaining

    def _wait(self, mask: int, deadline: float | None = None, *, cleanup: bool = False) -> None:
        if self._selector is None:
            raise DeviceError("HCI controller is closed")
        if self._sock is None:
            raise DeviceError("HCI controller is closed")
        self._selector.modify(self._sock, mask)
        while not self._selector.select(self._remaining(deadline, cleanup=cleanup)):
            pass
        self._remaining(deadline, cleanup=cleanup)

    def _send_packet(self, packet: bytes, deadline: float | None = None, *, cleanup: bool = False) -> None:
        if self._sock is None:
            raise DeviceError("HCI controller is closed")
        if not packet:
            raise DeviceError("empty HCI packet")
        while True:
            self._wait(selectors.EVENT_WRITE, deadline, cleanup=cleanup)
            try:
                sent = self._sock.send(packet)
            except BlockingIOError:
                continue
            if sent != len(packet):
                raise DeviceError("HCI packet write was incomplete")
            return

    def event(self, _deadline: float | None = None, *, _cleanup: bool = False) -> tuple[int, bytes]:
        """Read one structurally valid HCI event (event code, event payload)."""
        if self._sock is None:
            raise DeviceError("HCI controller is closed")
        while True:
            self._wait(selectors.EVENT_READ, _deadline, cleanup=_cleanup)
            try:
                packet, _ancillary, flags, _address = self._sock.recvmsg(MAX_EVENT_BYTES + 1)
            except BlockingIOError:
                continue
            if flags & socket.MSG_TRUNC or len(packet) > MAX_EVENT_BYTES:
                raise DeviceError("oversized HCI event")
            if len(packet) < 3 or packet[0] != 0x04 or packet[2] != len(packet) - 3:
                raise DeviceError("malformed HCI event packet")
            self._events += 1
            if self._events > MAX_EVENTS:
                raise DeviceError("HCI event limit exceeded")
            return packet[1], packet[3:]

    def command(
        self, opcode: int, params: bytes, *, _deadline: float | None = None, _cleanup: bool = False
    ) -> bytes:
        """Send one command and return its status-free Command Complete data."""
        if isinstance(opcode, bool) or not isinstance(opcode, int) or not 0 <= opcode <= 0xFFFF:
            raise DeviceError("invalid HCI opcode")
        if not isinstance(params, bytes) or len(params) > MAX_COMMAND_PARAMS:
            raise DeviceError("HCI command parameters exceed 255 bytes")
        command_deadline = time.monotonic() + COMMAND_TIMEOUT
        if not _cleanup:
            command_deadline = min(self.deadline, command_deadline)
        if _deadline is not None:
            command_deadline = min(command_deadline, _deadline)
        self._send_packet(
            b"\x01" + opcode.to_bytes(2, "little") + bytes((len(params),)) + params,
            command_deadline,
            cleanup=_cleanup,
        )
        while True:
            event, payload = self.event(command_deadline, _cleanup=_cleanup)
            if event == 0x10:
                raise DeviceError("controller reported a hardware error")
            if event == 0x0F:
                if len(payload) != 4:
                    raise DeviceError("malformed HCI Command Status")
                status = payload[0]
                reported_opcode = int.from_bytes(payload[2:4], "little")
                if reported_opcode != opcode:
                    raise DeviceError("unexpected HCI command status opcode")
                raise DeviceError(f"unexpected HCI Command Status ({status})")
            if event != 0x0E:
                # Scanning may produce LE reports while a command is pending.
                allowed_subevent = 0x0D if self.extended else 0x02
                if event == 0x3E and payload[:1] == bytes((allowed_subevent,)):
                    continue
                raise DeviceError("unexpected HCI event while awaiting command completion")
            if len(payload) < 4:
                raise DeviceError("malformed HCI Command Complete")
            reported_opcode = int.from_bytes(payload[1:3], "little")
            if reported_opcode != opcode:
                raise DeviceError("unexpected HCI command completion opcode")
            status = payload[3]
            if status != 0:
                raise DeviceError(f"HCI command failed ({status})")
            if payload[0] == 0:
                raise DeviceError("controller returned no credit for the next HCI command")
            return payload[4:]

    def _command_empty(
        self,
        opcode: int,
        params: bytes,
        *,
        _deadline: float | None = None,
        _cleanup: bool = False,
    ) -> None:
        if self.command(opcode, params, _deadline=_deadline, _cleanup=_cleanup):
            raise DeviceError("unexpected return parameters in HCI Command Complete")

    def setup_scan(self) -> None:
        if self.extended:
            params = b"\x00\x00\x01\x00\x10\x00\x10\x00"
            self._command_empty(0x2041, params)
            self._scanning = True
            self._command_empty(0x2042, b"\x01\x00\0\0\0\0")
            return
        self._command_empty(0x200B, b"\x00\x10\x00\x10\x00\x00\x00")
        self._scanning = True
        self._command_empty(0x200C, b"\x01\x00")

    def setup_advertise(self) -> None:
        if self.extended:
            self._command_empty(0x2039, b"\x00\x00")
            self._advertising = False
            params = (
                b"\x00\x00\x00"
                + b"\xa0\x00\x00"
                + b"\xa0\x00\x00"
                + b"\x07\x00\x00"
                + b"\0" * 6
                + b"\x00\x7f\x01\x00\x01\x00\x00"
            )
            selected_power = self.command(0x2036, params)
            if len(selected_power) != 1 or not (
                -127 <= int.from_bytes(selected_power, "little", signed=True) <= 20
            ):
                raise DeviceError("controller returned invalid extended advertising transmit power")
            self._advertising_configured = True
            return
        self._command_empty(0x200A, b"\x00")
        # 0x03 is non-connectable, non-scannable legacy advertising.
        params = (
            b"\xa0\x00\xa0\x00\x03\x00\x00"
            + b"\0" * 6
            + b"\x07\x00"
        )
        self._command_empty(0x2006, params)
        self._advertising_configured = True

    def advertise(self, data: bytes) -> None:
        if not self._advertising_configured:
            raise DeviceError("advertising parameters have not been configured")
        maximum = 202 if self.extended else 31
        if not isinstance(data, bytes) or len(data) > maximum:
            raise DeviceError(f"advertising data must be at most {maximum} bytes")
        if self.extended:
            self._command_empty(0x2039, b"\x00\x00")
            self._advertising = False
            params = b"\x00\x03\x01" + bytes((len(data),)) + data
            self._command_empty(0x2037, params)
            self._advertising = True
            self._command_empty(0x2039, b"\x01\x01\x00\x00\x00\x00")
            return
        self._command_empty(0x200A, b"\x00")
        self._advertising = False
        self._command_empty(0x2008, bytes((len(data),)) + data.ljust(31, b"\0"))
        self._advertising = True
        self._command_empty(0x200A, b"\x01")

    def dwell(self, seconds: float) -> None:
        if (
            isinstance(seconds, bool)
            or not isinstance(seconds, (int, float))
            or not math.isfinite(seconds)
            or seconds < 0
        ):
            raise DeviceError("invalid HCI dwell duration")
        end = time.monotonic() + seconds
        if end > self.deadline:
            raise DeviceError("HCI dwell exceeds the controller deadline")
        if self._selector is None or self._sock is None:
            raise DeviceError("HCI controller is closed")
        self._selector.modify(self._sock, selectors.EVENT_READ)
        while True:
            remaining = end - time.monotonic()
            if remaining <= 0:
                if time.monotonic() >= self.deadline:
                    raise DeviceError("HCI controller deadline expired during dwell")
                return
            if self._selector.select(remaining):
                self.event(end)
                raise DeviceError("unexpected HCI event during dwell")
            if time.monotonic() >= self.deadline:
                raise DeviceError("HCI controller deadline expired during dwell")

    def _close_descriptors(self) -> list[OSError | ValueError]:
        selector, self._selector = self._selector, None
        sock, self._sock = self._sock, None
        errors: list[OSError | ValueError] = []
        if selector is not None:
            try:
                selector.close()
            except (OSError, ValueError) as err:
                errors.append(err)
        if sock is not None:
            try:
                sock.close()
            except (OSError, ValueError) as err:
                errors.append(err)
        return errors

    def close(self) -> None:
        errors: list[BaseException] = []
        old_mask: set[signal.Signals] | None = None
        try:
            try:
                old_mask = signal.pthread_sigmask(signal.SIG_BLOCK, INTERRUPT_SIGNALS)
            except OSError as err:
                errors.append(err)
            cleanup_deadline = time.monotonic() + 1.0
            if self._advertising:
                try:
                    if self.extended:
                        self._command_empty(0x2039, b"\x00\x00", _deadline=cleanup_deadline, _cleanup=True)
                    else:
                        self._command_empty(0x200A, b"\x00", _deadline=cleanup_deadline, _cleanup=True)
                except (OSError, DeviceError) as err:
                    errors.append(err)
                self._advertising = False
            if self._scanning:
                try:
                    if self.extended:
                        self._command_empty(
                            0x2042,
                            b"\x00\x00\0\0\0\0",
                            _deadline=cleanup_deadline,
                            _cleanup=True,
                        )
                    else:
                        self._command_empty(0x200C, b"\x00\x00", _deadline=cleanup_deadline, _cleanup=True)
                except (OSError, DeviceError) as err:
                    errors.append(err)
                self._scanning = False
        finally:
            errors.extend(self._close_descriptors())
            if old_mask is not None:
                try:
                    signal.pthread_sigmask(signal.SIG_SETMASK, old_mask)
                except OSError as err:
                    errors.append(err)
        if errors:
            raise DeviceError("HCI controller cleanup failed") from errors[0]
