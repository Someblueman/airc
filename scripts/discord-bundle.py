#!/usr/bin/env python3
"""Transfer one signed AIRC bundle through a fixed Discord channel."""

from __future__ import annotations

import argparse
import base64
import binascii
import datetime as dt
import hashlib
import http.client
import json
import math
import os
import re
import signal
import socket
import ssl
import stat
import sys
import time
from typing import Any

from gateway_device_io import (
    DeviceError,
    interruption_handlers,
    read_regular,
    require_linux,
    run_child,
    unblock_child_interrupts,
    write_private,
)

MAX_BUNDLE = 1440
MAX_RESPONSE = 64 * 1024
MESSAGE_PREFIX = "AIRC_SIGNED_BUNDLE_V1:"
MAX_SNOWFLAKE = (1 << 64) - 1
ID_RE = re.compile(r"[1-9][0-9]{0,19}", re.ASCII)
TOKEN_RE = re.compile(rb"[A-Za-z0-9_.-]{20,512}", re.ASCII)
HOST = "discord.com"
USER_AGENT = "DiscordBot (https://github.com/Someblueman/airc, 1)"
HTTP_TIMEOUT = 5.0
PROCESS_TIMEOUT = 30


def valid_id(value: str) -> bool:
    return ID_RE.fullmatch(value) is not None and int(value) <= MAX_SNOWFLAKE


def read_token(path: str) -> str:
    if not os.path.isabs(path) or os.path.normpath(path) != path or os.path.realpath(path) != path:
        raise DeviceError("token path must be absolute, canonical and contain no symlink")
    try:
        before = os.lstat(path)
        if (
            not stat.S_ISREG(before.st_mode)
            or before.st_uid not in (0, os.geteuid())
            or stat.S_IMODE(before.st_mode) != 0o600
            or not 1 <= before.st_size <= 512
        ):
            raise DeviceError("token file must be a root/current-user-owned mode-0600 regular file")
        fd = os.open(path, os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK)
    except OSError as err:
        raise DeviceError("cannot open token file") from err
    try:
        opened = os.fstat(fd)
        if (
            not stat.S_ISREG(opened.st_mode)
            or (before.st_dev, before.st_ino) != (opened.st_dev, opened.st_ino)
            or opened.st_uid not in (0, os.geteuid())
            or stat.S_IMODE(opened.st_mode) != 0o600
            or not 1 <= opened.st_size <= 512
        ):
            raise DeviceError("token file changed or has unsafe metadata")
        raw = bytearray()
        while len(raw) < opened.st_size:
            part = os.read(fd, opened.st_size - len(raw))
            if not part:
                raise DeviceError("token file changed while being read")
            raw.extend(part)
        if os.read(fd, 1) or not TOKEN_RE.fullmatch(bytes(raw)):
            raise DeviceError("token file is outside the supported token format")
        return raw.decode("ascii")
    except OSError as err:
        raise DeviceError("cannot read token file") from err
    finally:
        os.close(fd)


def _pairs_no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise DeviceError("Discord response contains duplicate JSON keys")
        result[key] = value
    return result


def _reject_constant(_value: str) -> None:
    raise DeviceError("Discord response contains a non-finite JSON number")


def _finite_float(value: str) -> float:
    number = float(value)
    if not math.isfinite(number):
        raise DeviceError("Discord response contains a non-finite JSON number")
    return number


def request(token: str, method: str, path: str, body: bytes | None = None) -> dict[str, Any]:
    headers = {
        "Authorization": f"Bot {token}",
        "Accept": "application/json",
        "Accept-Encoding": "identity",
        "Connection": "close",
        "User-Agent": USER_AGENT,
    }
    if body is not None:
        headers["Content-Type"] = "application/json"
    connection = http.client.HTTPSConnection(HOST, timeout=HTTP_TIMEOUT, context=ssl.create_default_context())
    try:
        connection.request(method, path, body=body, headers=headers)
        response = connection.getresponse()
        encodings = response.headers.get_all("Content-Encoding", [])
        if len(encodings) > 1 or (encodings and encodings[0].strip().lower() not in ("", "identity")):
            raise DeviceError("Discord response encoding is unsupported")
        lengths = response.headers.get_all("Content-Length", [])
        if len(lengths) > 1 or (lengths and re.fullmatch(r"[0-9]{1,10}", lengths[0]) is None):
            raise DeviceError("Discord response length is invalid")
        transfer_encodings = response.headers.get_all("Transfer-Encoding", [])
        if len(transfer_encodings) > 1 or (
            transfer_encodings and transfer_encodings[0].strip().lower() != "chunked"
        ):
            raise DeviceError("Discord response transfer encoding is unsupported")
        if lengths and int(lengths[0]) > MAX_RESPONSE:
            raise DeviceError("Discord response exceeds its size bound")
        if lengths and transfer_encodings:
            raise DeviceError("Discord response has conflicting length headers")
        content_types = response.headers.get_all("Content-Type", [])
        if len(content_types) != 1 or content_types[0].split(";", 1)[0].strip().lower() != "application/json":
            raise DeviceError("Discord response is not JSON")
        data = response.read(MAX_RESPONSE + 1)
        if len(data) > MAX_RESPONSE or (lengths and len(data) != int(lengths[0])):
            raise DeviceError("Discord response exceeds its size bound or is incomplete")
        if response.status != 200:
            raise DeviceError(f"Discord API returned HTTP {response.status}")
        try:
            value = json.loads(
                data.decode("utf-8", "strict"),
                object_pairs_hook=_pairs_no_duplicates,
                parse_constant=_reject_constant,
                parse_float=_finite_float,
            )
        except (UnicodeDecodeError, json.JSONDecodeError, ValueError, RecursionError) as err:
            raise DeviceError("Discord response JSON is invalid") from err
        if not isinstance(value, dict):
            raise DeviceError("Discord response JSON is not an object")
        return value
    except (http.client.HTTPException, OSError, ssl.SSLError, socket.timeout) as err:
        raise DeviceError("Discord API request failed") from err
    finally:
        connection.close()


def verify_bot_and_channel(token: str, bot_id: str, guild: str, channel: str) -> None:
    bot = request(token, "GET", "/api/v10/users/@me")
    if bot.get("id") != bot_id or bot.get("bot") is not True:
        raise DeviceError("Discord token does not identify the configured bot")
    room = request(token, "GET", f"/api/v10/channels/{channel}")
    if room.get("id") != channel or room.get("guild_id") != guild or type(room.get("type")) is not int or room["type"] != 0:
        raise DeviceError("Discord channel is not the configured guild text channel")


def fresh_timestamp(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        timestamp = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return False
    if timestamp.tzinfo is None or timestamp.utcoffset() != dt.timedelta(0):
        return False
    age = time.time() - timestamp.timestamp()
    return -5.0 <= age <= 300.0


def validate_message(
    message: dict[str, Any], guild: str, channel: str, author: str, *,
    expected_content: str | None = None, expected_nonce: str | None = None,
    expected_id: str | None = None, require_bot_author: bool = False,
) -> str:
    message_id = message.get("id")
    if (
        not isinstance(message_id, str)
        or not valid_id(message_id)
        or (expected_id is not None and message_id != expected_id)
        or message.get("channel_id") != channel
        or ("guild_id" in message and message.get("guild_id") != guild)
        or type(message.get("type")) is not int
        or message["type"] != 0
    ):
        raise DeviceError("Discord message identity or type is unexpected")
    user = message.get("author")
    if (
        not isinstance(user, dict)
        or user.get("id") != author
        or (require_bot_author and user.get("bot") is not True)
    ):
        raise DeviceError("Discord message author is unexpected")
    if (
        "edited_timestamp" not in message
        or message.get("edited_timestamp") is not None
        or message.get("webhook_id") is not None
        or message.get("message_reference") is not None
        or message.get("referenced_message") is not None
        or message.get("application_id") is not None
        or message.get("interaction") is not None
        or message.get("interaction_metadata") is not None
        or type(message.get("flags", 0)) is not int
        or message.get("flags", 0) != 0
        or message.get("attachments") != []
        or message.get("embeds") != []
        or message.get("components", []) != []
        or message.get("sticker_items", []) != []
        or message.get("poll") is not None
    ):
        raise DeviceError("Discord message contains edits or unsupported metadata")
    if not fresh_timestamp(message.get("timestamp")):
        raise DeviceError("Discord message timestamp is outside the freshness window")
    content = message.get("content")
    if not isinstance(content, str) or (expected_content is not None and content != expected_content):
        raise DeviceError("Discord message content does not match the transfer")
    if expected_nonce is not None and "nonce" in message and message["nonce"] != expected_nonce:
        raise DeviceError("Discord message nonce does not match the transfer")
    return content


def encode_payload(bundle: bytes) -> str:
    if not 1 <= len(bundle) <= MAX_BUNDLE:
        raise DeviceError("signed bundle must contain 1..1440 bytes")
    return MESSAGE_PREFIX + base64.b64encode(bundle).decode("ascii")


def decode_payload(content: str) -> bytes:
    if len(content) > 2000:
        raise DeviceError("Discord message exceeds the content limit")
    if not content.startswith(MESSAGE_PREFIX):
        raise DeviceError("Discord message does not contain the bundle format")
    encoded = content[len(MESSAGE_PREFIX) :]
    try:
        raw = base64.b64decode(encoded.encode("ascii"), validate=True)
    except (UnicodeEncodeError, binascii.Error, ValueError) as err:
        raise DeviceError("Discord bundle encoding is invalid") from err
    if not 1 <= len(raw) <= MAX_BUNDLE or base64.b64encode(raw).decode("ascii") != encoded:
        raise DeviceError("Discord bundle encoding is noncanonical or out of bounds")
    return raw


def child(args: argparse.Namespace) -> None:
    unblock_child_interrupts()
    signal.signal(signal.SIGALRM, signal.SIG_DFL)
    signal.alarm(PROCESS_TIMEOUT)
    require_linux()
    for value in (args.bot_id, args.guild_id, args.channel_id):
        if not valid_id(value):
            raise DeviceError("Discord bot, guild and channel IDs must be canonical nonzero decimal snowflakes")
    if args.command == "_read-child" and (not valid_id(args.author_id) or not valid_id(args.message_id)):
        raise DeviceError("Discord author and message IDs must be canonical nonzero decimal snowflakes")
    if args.command == "_emit-child":
        bundle = read_regular(args.input, MAX_BUNDLE)
        content = encode_payload(bundle)
    else:
        bundle = None
    token = read_token(args.token_file)
    verify_bot_and_channel(token, args.bot_id, args.guild_id, args.channel_id)
    if args.command == "_emit-child":
        assert bundle is not None
        nonce = hashlib.sha256(
            b"airc-discord-v1\0" + args.guild_id.encode("ascii") + b"\0" +
            args.channel_id.encode("ascii") + b"\0" + bundle
        ).hexdigest()[:24]
        body = json.dumps(
            {"content": content, "allowed_mentions": {"parse": []}, "tts": False,
             "nonce": nonce, "enforce_nonce": True},
            separators=(",", ":"), ensure_ascii=True,
        ).encode("ascii")
        message = request(token, "POST", f"/api/v10/channels/{args.channel_id}/messages", body)
        validate_message(message, args.guild_id, args.channel_id, args.bot_id,
                         expected_content=content, expected_nonce=nonce, require_bot_author=True)
        print(json.dumps({"discord_message_id": message["id"]}, separators=(",", ":")))
        return
    message = request(token, "GET", f"/api/v10/channels/{args.channel_id}/messages/{args.message_id}")
    content = validate_message(
        message, args.guild_id, args.channel_id, args.author_id, expected_id=args.message_id
    )
    data = decode_payload(content)
    offset = 0
    while offset < len(data):
        count = os.write(sys.stdout.fileno(), data[offset:])
        if count <= 0:
            raise DeviceError("Discord child output made no progress")
        offset += count


def add_common(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--token-file", required=True)
    parser.add_argument("--bot-id", required=True)
    parser.add_argument("--guild-id", required=True)
    parser.add_argument("--channel-id", required=True)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("read", "emit", "_read-child", "_emit-child"):
        command = sub.add_parser(name, help=argparse.SUPPRESS if name.startswith("_") else name)
        add_common(command)
        if name in ("read", "_read-child"):
            command.add_argument("--author-id", required=True)
            command.add_argument("--message-id", required=True)
        if name == "read":
            command.add_argument("--output", required=True)
        if name in ("emit", "_emit-child"):
            command.add_argument("--input", required=True)
    args = parser.parse_args(argv)
    with interruption_handlers():
        try:
            require_linux()
            if args.command.startswith("_"):
                child(args)
                return 0
            command = [sys.executable, os.path.abspath(__file__), f"_{args.command}-child"]
            common = ("token_file", "bot_id", "guild_id", "channel_id")
            for name in common:
                command.extend((f"--{name.replace('_', '-')}", getattr(args, name)))
            if args.command == "emit":
                output = run_child(command + ["--input", args.input], 1024)
                if not output.endswith(b"\n") or len(output) > 1024:
                    raise DeviceError("Discord child returned invalid acceptance metadata")
                sys.stdout.buffer.write(output)
                sys.stdout.buffer.flush()
            else:
                command.extend(("--author-id", args.author_id, "--message-id", args.message_id))
                data = run_child(command, MAX_BUNDLE)
                write_private(args.output, data, MAX_BUNDLE)
            return 0
        except DeviceError as err:
            print(f"discord-bundle: {err}", file=sys.stderr)
            return 1
        except (OSError, ValueError, KeyboardInterrupt):
            print("discord-bundle: operation failed or interrupted", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
