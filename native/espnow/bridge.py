"""One-shot, dedicated ESP32 ESP-NOW bundle bridge (MicroPython 1.26.0)."""

import hashlib
import network
import sys
import time
import espnow
from machine import Pin, UART, WDT


MAGIC = b"AEU1"
AUTH_DOMAIN = b"airc-espnow-unicast-v1\x00"
MAX_BUNDLE = 2048
CHUNK_BYTES = 176
RECORD_BYTES = 248
MAX_FRAGMENTS = 12
MAX_RECEIVE_CALLS = 256
EMPTY_DIGEST = bytes(32)
ZERO_MAC = bytes(6)
UART_TIMEOUT_MS = 100
TRANSACTION_MS = 25000


def _require(condition, message):
    if not condition:
        raise ValueError(message)


def _valid_mac(mac):
    return (
        isinstance(mac, bytes)
        and len(mac) == 6
        and mac != ZERO_MAC
        and mac != b"\xff\xff\xff\xff\xff\xff"
        and (mac[0] & 1) == 0
    )


def _load_config():
    import espnow_config as cfg

    version = sys.implementation.version
    _require(sys.implementation.name == "micropython", "wrong runtime")
    _require(tuple(version[:3]) == (1, 26, 0), "wrong MicroPython version")
    _require(_valid_mac(cfg.LOCAL_MAC), "invalid local MAC")
    _require(_valid_mac(cfg.PEER_MAC), "invalid peer MAC")
    _require(cfg.LOCAL_MAC != cfg.PEER_MAC, "MAC addresses must differ")
    _require(type(cfg.CHANNEL) is int and 1 <= cfg.CHANNEL <= 11, "invalid channel")
    _require(isinstance(cfg.PMK, bytes) and len(cfg.PMK) == 16, "invalid PMK")
    _require(isinstance(cfg.LMK, bytes) and len(cfg.LMK) == 16, "invalid LMK")
    _require(isinstance(cfg.AUTH_KEY, bytes) and len(cfg.AUTH_KEY) == 32, "invalid auth key")
    _require(cfg.ROLE in ("read", "emit"), "invalid role")
    return cfg


def _deadline_remaining(deadline):
    remaining = time.ticks_diff(deadline, time.ticks_ms())
    if remaining <= 0:
        raise ValueError("transaction timed out")
    return remaining


def _uart_read_exact(uart, length, deadline):
    _require(0 <= length <= MAX_BUNDLE, "UART read exceeds bound")
    result = bytearray(length)
    offset = 0
    while offset < length:
        _deadline_remaining(deadline)
        part = uart.read(length - offset)
        if part:
            count = len(part)
            if count > length - offset:
                raise ValueError("unexpected UART input")
            result[offset : offset + count] = part
            offset += count
    _deadline_remaining(deadline)
    return bytes(result)


def _uart_write_all(uart, data, deadline):
    offset = 0
    while offset < len(data):
        _deadline_remaining(deadline)
        written = uart.write(data[offset:])
        if written is None or written == 0:
            continue
        if written < 0 or written > len(data) - offset:
            raise ValueError("invalid UART write result")
        offset += written
    _deadline_remaining(deadline)


def _response(uart, nonce, status, length, digest, deadline):
    if len(nonce) != 16 or len(digest) != 32:
        raise ValueError("invalid response state")
    _uart_write_all(uart, MAGIC + nonce + status + length.to_bytes(2, "big") + digest, deadline)


def _parse_request(raw, cfg):
    if len(raw) != 68:
        raise ValueError("invalid request length")
    _require(raw[:4] == MAGIC, "invalid request")
    _require(raw[20:26] == cfg.LOCAL_MAC, "wrong local address")
    _require(raw[26:32] == cfg.PEER_MAC, "wrong peer address")
    _require(raw[32] == cfg.CHANNEL, "wrong channel")
    operation = raw[33:34]
    expected_operation = b"R" if cfg.ROLE == "read" else b"T"
    _require(operation == expected_operation, "wrong operation for board role")
    length = int.from_bytes(raw[34:36], "big")
    digest = raw[36:68]
    if operation == b"R":
        _require(length == 0 and digest == EMPTY_DIGEST, "invalid read request")
    else:
        _require(1 <= length <= MAX_BUNDLE, "invalid upload length")
    return operation, length, digest


def _hmac_sha256(key, message):
    padded = key + bytes(64 - len(key))
    inner_pad = bytes((value ^ 0x36) for value in padded)
    outer_pad = bytes((value ^ 0x5c) for value in padded)
    return hashlib.sha256(outer_pad + hashlib.sha256(inner_pad + message).digest()).digest()


def _constant_equal(left, right):
    if len(left) != len(right):
        return False
    difference = 0
    for index in range(len(left)):
        difference |= left[index] ^ right[index]
    return difference == 0


def _record_tag(cfg, sender, receiver, record):
    if len(record) != 216:
        raise ValueError("invalid RF record size")
    context = AUTH_DOMAIN + sender + receiver + bytes((cfg.CHANNEL,)) + record
    return _hmac_sha256(cfg.AUTH_KEY, context)


def _make_record(cfg, total, index, digest, chunk):
    if len(chunk) > CHUNK_BYTES:
        raise ValueError("RF chunk exceeds bound")
    body = MAGIC + total.to_bytes(2, "big") + index.to_bytes(2, "big") + digest
    body += chunk + bytes(CHUNK_BYTES - len(chunk))
    return body + _record_tag(cfg, cfg.LOCAL_MAC, cfg.PEER_MAC, body)


def _verify_record(cfg, packet):
    if len(packet) != RECORD_BYTES or packet[:4] != MAGIC:
        raise ValueError("malformed RF record")
    body, tag = packet[:216], packet[216:]
    expected = _record_tag(cfg, cfg.PEER_MAC, cfg.LOCAL_MAC, body)
    if not _constant_equal(tag, expected):
        raise ValueError("RF authentication failed")
    total = int.from_bytes(body[4:6], "big")
    index = int.from_bytes(body[6:8], "big")
    _require(1 <= total <= MAX_BUNDLE, "invalid RF bundle size")
    count = (total + CHUNK_BYTES - 1) // CHUNK_BYTES
    _require(count <= MAX_FRAGMENTS and index < count, "invalid RF fragment index")
    return total, index, body[8:40], body[40:216]


def _radio_start(cfg):
    sta = network.WLAN(network.WLAN.IF_STA)
    ap = network.WLAN(network.WLAN.IF_AP)
    e = None
    try:
        ap.active(False)
        sta.active(False)
        sta.active(True)
        sta.disconnect()
        _require(not sta.isconnected(), "STA unexpectedly connected")
        sta.config(channel=cfg.CHANNEL)
        _require(sta.config("mac") == cfg.LOCAL_MAC, "STA MAC setup failed")
        _require(sta.config("channel") == cfg.CHANNEL, "STA channel setup failed")
        e = espnow.ESPNow()
        e.active(False)
        e.config(rxbuf=4096)
        e.active(True)
        e.set_pmk(cfg.PMK)
        e.add_peer(
            cfg.PEER_MAC,
            lmk=cfg.LMK,
            channel=cfg.CHANNEL,
            ifidx=network.WLAN.IF_STA,
            encrypt=True,
        )
        peer = e.get_peer(cfg.PEER_MAC)
        _require(peer is not None, "peer setup failed")
        _require(peer[0] == cfg.PEER_MAC, "peer address mismatch")
        _require(peer[1] == cfg.LMK, "peer key setup failed")
        _require(peer[2] == cfg.CHANNEL, "peer channel setup failed")
        _require(peer[3] == network.WLAN.IF_STA, "peer interface setup failed")
        _require(peer[4] is True, "peer encryption setup failed")
        _require(e.peer_count() == (1, 1), "unexpected peer table")
        return sta, ap, e
    except Exception:
        _radio_stop(sta, ap, e)
        raise


def _radio_stop(sta, ap, e):
    failure = None
    if e is not None:
        try:
            e.active(False)
        except Exception as error:
            failure = error
    for interface in (sta, ap):
        if interface is None:
            continue
        try:
            interface.active(False)
        except Exception as error:
            if failure is None:
                failure = error
    if failure is not None:
        raise failure


def _send_bundle(cfg, e, data, digest, deadline):
    count = (len(data) + CHUNK_BYTES - 1) // CHUNK_BYTES
    _require(1 <= count <= MAX_FRAGMENTS, "invalid RF bundle size")
    for index in range(count):
        _deadline_remaining(deadline)
        start = index * CHUNK_BYTES
        chunk = data[start : start + CHUNK_BYTES]
        packet = _make_record(cfg, len(data), index, digest, chunk)
        if e.send(cfg.PEER_MAC, packet, True) is not True:
            raise ValueError("RF peer did not acknowledge fragment")
        _deadline_remaining(deadline)


def _receive_bundle(cfg, e, deadline):
    receive_buffers = [None, bytearray(250)]
    pieces = []
    expected_count = None
    expected_total = None
    expected_digest = None
    receive_calls = 0
    while expected_count is None or len(pieces) < expected_count:
        _deadline_remaining(deadline)
        if receive_calls >= MAX_RECEIVE_CALLS:
            raise ValueError("too many RF receive calls")
        # Count every call: a zero-length packet also returns zero, but can
        # still insert a source MAC into MicroPython's fixed RSSI dictionary.
        receive_calls += 1
        length = e.recvinto(receive_buffers, UART_TIMEOUT_MS)
        _deadline_remaining(deadline)
        if length == 0:
            continue
        _require(1 <= length <= 250, "invalid RF receive length")
        source = bytes(receive_buffers[0])
        payload = bytes(receive_buffers[1][:length])
        if source != cfg.PEER_MAC:
            continue
        total, index, digest, chunk = _verify_record(cfg, payload)
        if expected_count is None:
            expected_total = total
            expected_digest = digest
            expected_count = (total + CHUNK_BYTES - 1) // CHUNK_BYTES
        _require(total == expected_total, "mixed RF transfers")
        _require(digest == expected_digest, "mixed RF digests")
        _require(index == len(pieces), "duplicate or out-of-order RF record")
        used = min(CHUNK_BYTES, expected_total - index * CHUNK_BYTES)
        _require(chunk[used:] == bytes(CHUNK_BYTES - used), "nonzero RF padding")
        pieces.append(chunk[:used])
    data = b"".join(pieces)
    _require(len(data) == expected_total, "incomplete RF bundle")
    _require(hashlib.sha256(data).digest() == expected_digest, "RF bundle digest mismatch")
    return data, expected_digest


def _make_uart():
    return UART(
        1,
        baudrate=115200,
        bits=8,
        parity=None,
        stop=1,
        tx=Pin(33),
        rx=Pin(32),
        timeout=UART_TIMEOUT_MS,
        timeout_char=UART_TIMEOUT_MS,
        rxbuf=4096,
        txbuf=512,
        flow=0,
    )


def _radio_idle():
    failure = None
    try:
        e = espnow.ESPNow()
        e.active(False)
        _require(len(e.peers_table) == 0, "reset required to clear ESP-NOW peer state")
    except Exception as error:
        failure = error
    for interface_id in (network.WLAN.IF_AP, network.WLAN.IF_STA):
        try:
            network.WLAN(interface_id).active(False)
        except Exception as error:
            if failure is None:
                failure = error
    if failure is not None:
        raise failure


def main():
    _radio_idle()
    cfg = _load_config()
    uart = _make_uart()
    sta = None
    ap = None
    radio = None
    nonce = None
    deadline = None
    wdt_armed = False
    try:
        first = None
        while first is None:
            first = uart.read(1)
        watchdog = WDT(timeout=30000)
        wdt_armed = True
        deadline = time.ticks_add(time.ticks_ms(), TRANSACTION_MS)
        safe_error_reply = False
        try:
            prefix = first + _uart_read_exact(uart, 19, deadline)
            nonce = prefix[4:20]
            safe_error_reply = True
            raw = prefix + _uart_read_exact(uart, 48, deadline)
            operation, length, digest = _parse_request(raw, cfg)
            sta, ap, radio = _radio_start(cfg)
            safe_error_reply = False
            _response(uart, nonce, b"R", 0, EMPTY_DIGEST, deadline)
            safe_error_reply = True
            if operation == b"T":
                uploaded = _uart_read_exact(uart, length, deadline)
                _require(hashlib.sha256(uploaded).digest() == digest, "UART upload digest mismatch")
                _send_bundle(cfg, radio, uploaded, digest, deadline)
                result = b""
                result_digest = EMPTY_DIGEST
            else:
                result, result_digest = _receive_bundle(cfg, radio, deadline)
            _radio_stop(sta, ap, radio)
            sta = ap = radio = None
            safe_error_reply = False
            if operation == b"R":
                _response(uart, nonce, b"O", len(result), result_digest, deadline)
                _uart_write_all(uart, result, deadline)
            else:
                _response(uart, nonce, b"O", 0, EMPTY_DIGEST, deadline)
        except Exception:
            try:
                _radio_stop(sta, ap, radio)
            except Exception:
                pass
            sta = ap = radio = None
            if nonce is not None and safe_error_reply:
                try:
                    _response(uart, nonce, b"E", 0, EMPTY_DIGEST, deadline)
                    safe_error_reply = False
                except Exception:
                    pass
    finally:
        try:
            _radio_stop(sta, ap, radio)
        except Exception:
            pass
        try:
            uart.flush()
        except Exception:
            pass
        try:
            uart.deinit()
        except Exception:
            pass
    if wdt_armed:
        while True:
            time.sleep_ms(1000)


try:
    main()
except Exception:
    try:
        _radio_idle()
    except Exception:
        pass
    while True:
        time.sleep_ms(1000)
