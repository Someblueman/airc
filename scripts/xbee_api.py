"""Bounded XBee API mode 2 framing over the shared raw serial link."""

from __future__ import annotations

import secrets

from gateway_device_io import DeviceError
from gateway_serial import SerialLink

_ESCAPED = frozenset((0x7E, 0x7D, 0x11, 0x13))
_MAX_FRAME = 256
_MAX_NOISE = 4096


def _escape(data: bytes) -> bytes:
    out = bytearray()
    for value in data:
        if value in _ESCAPED:
            out.extend((0x7D, value ^ 0x20))
        else:
            out.append(value)
    return bytes(out)


class XBeeLink(SerialLink):
    """Add the API2 codec and per-link frame IDs to an owned UART."""

    def __init__(self, path: str) -> None:
        super().__init__(path)
        self._next_id = secrets.randbelow(255) + 1
        self._allocated_ids = 0

    def __enter__(self) -> XBeeLink:
        super().__enter__()
        return self

    def next_frame_id(self) -> int:
        if self.fd < 0:
            raise DeviceError("serial link is closed")
        if self._allocated_ids >= 255:
            raise DeviceError("XBee frame ID space exhausted for this link")
        frame_id = self._next_id
        self._allocated_ids += 1
        self._next_id = 1 if frame_id == 255 else frame_id + 1
        return frame_id

    def send_frame(self, payload: bytes) -> None:
        if not isinstance(payload, bytes) or not 1 <= len(payload) <= _MAX_FRAME:
            raise DeviceError("XBee API frame must contain 1 to 256 bytes")
        length = len(payload).to_bytes(2, "big")
        checksum = bytes(((0xFF - (sum(payload) & 0xFF)) & 0xFF,))
        packet = b"\x7e" + _escape(length + payload + checksum)
        self.write(packet)

    def _read_unescaped(self) -> int:
        value = super().read_byte()
        if value == 0x7E:
            raise DeviceError("truncated XBee API frame")
        if value == 0x7D:
            escaped = super().read_byte()
            decoded = escaped ^ 0x20
            if decoded not in _ESCAPED:
                raise DeviceError("noncanonical XBee API escape")
            return decoded
        if value in (0x11, 0x13):
            raise DeviceError("unescaped reserved byte in XBee API frame")
        return value

    def read_frame(self) -> bytes:
        self.verify_path()
        noise = 0
        while True:
            if super().read_byte() == 0x7E:
                break
            noise += 1
            if noise > _MAX_NOISE:
                raise DeviceError("too much noise before XBee API delimiter")

        length = (self._read_unescaped() << 8) | self._read_unescaped()
        if not 1 <= length <= _MAX_FRAME:
            raise DeviceError("XBee API frame length is outside its bound")
        payload = bytes(self._read_unescaped() for _ in range(length))
        checksum = self._read_unescaped()
        if (sum(payload) + checksum) & 0xFF != 0xFF:
            raise DeviceError("XBee API frame checksum is invalid")
        self.verify_path()
        return payload
