"""Exec a pinned minimodem process for one bounded acoustic transfer."""

from __future__ import annotations

import os
import re
import signal
from typing import NoReturn

from gateway_device_io import DeviceError, require_linux, unblock_child_interrupts
from gateway_native_io import install_stdin, open_native

_MAX_FRAME_BYTES = 2810
_DEVICE_RE = re.compile(r"plughw:(0|[1-9][0-9]{0,2}),(0|[1-9][0-9]{0,2})\Z", re.ASCII)


def _validate_device(device: str) -> None:
    if len(device.encode("ascii", errors="ignore")) > 31 or not device.isascii():
        raise DeviceError("ALSA device selector must be at most 31 ASCII bytes")
    match = _DEVICE_RE.fullmatch(device)
    if match is None or any(int(value) > 255 for value in match.groups()):
        raise DeviceError("ALSA device must be canonical plughw:N,M with N and M from 0 to 255")


def exec_modem(path: str, digest: str, device: str, mode: str, frame: bytes | None) -> NoReturn:
    """Replace the supervised child with the pinned modem binary."""
    require_linux()
    unblock_child_interrupts()
    signal.signal(signal.SIGALRM, signal.SIG_DFL)
    signal.alarm(30)

    _validate_device(device)
    if mode not in ("rx", "tx"):
        raise DeviceError("mode must be rx or tx")
    if (mode == "rx" and frame is not None) or (mode == "tx" and frame is None):
        raise DeviceError("frame must be absent for receive and present for transmit")

    modem_fd = open_native(path, digest)
    try:
        install_stdin(frame, _MAX_FRAME_BYTES)
        direction = ["--rx", "--rx-one"] if mode == "rx" else ["--tx", "--volume=0.5"]
        argv = [
            "minimodem",
            "--quiet",
            "--ascii",
            "--startbits=1",
            "--stopbits=1",
            "--samplerate=48000",
            "--mark=1200",
            "--space=2200",
            f"--alsa={device}",
            *direction,
            "1200",
        ]
        # minimodem uses SIGALRM for transmitter carrier shutdown; the parent
        # owns the hard process deadline once this exec replaces the child.
        signal.alarm(0)
        os.execve(modem_fd, argv, os.environ)
    except OSError as err:
        os.close(modem_fd)
        raise DeviceError("cannot exec pinned minimodem binary") from err
    except BaseException:
        os.close(modem_fd)
        raise
    raise DeviceError("minimodem exec returned unexpectedly")
