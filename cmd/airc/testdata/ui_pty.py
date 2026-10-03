"""Exercise the real UI on a PTY. No third-party terminal library is needed."""
import fcntl
import json
import os
import re
import select
import struct
import subprocess
import sys
import termios
import time
import threading

binary, address, question = sys.argv[1:]
master, slave = os.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 45, 160, 0, 0))
before = termios.tcgetattr(slave)
process = subprocess.Popen(
    [binary, "ui", "--addr", address, "--nick", "me", "--channel", "#room"],
    stdin=slave, stdout=slave, stderr=slave, start_new_session=True,
)
ansi = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")
received = bytearray()
reading = threading.Event()
reading.set()


def drain():
    while reading.is_set():
        if select.select([master], [], [], 0.05)[0]:
            try:
                received.extend(os.read(master, 65536))
            except OSError:
                return


reader = threading.Thread(target=drain, daemon=True)
reader.start()


def read_until(text, start=0):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if text in ansi.sub("", received[start:].decode("utf-8", errors="replace")):
            return
        time.sleep(0.01)
        if process.poll() is not None:
            raise AssertionError(f"UI exited: {process.returncode}")
    raise AssertionError(f"UI never displayed {text!r}: {received[-4000:]!r}")


def cli(*args):
    result = subprocess.run(
        [binary, *args, "--addr", address], capture_output=True, text=True, timeout=10
    )
    if result.returncode:
        raise AssertionError(result.stderr)
    return result.stdout


def posts():
    return [json.loads(line) for line in cli("history", "#room", "--limit", "64", "--json").splitlines()]


def write(data, split=False):
    if split:
        for byte in data:
            os.write(master, bytes([byte]))
            time.sleep(0.003)
    else:
        os.write(master, data)


try:
    read_until("online")
    assert b"\x1b[?2004h" in received, "bracketed paste was not enabled"
    # Selection is the scorecard starting state, outside the participant action count.
    for _ in range(14):
        write(b"\x1b[1;5A", split=True)
    read_until("Selected " + question)
    start = len(received)
    began = time.monotonic()
    write(b"\x0f")  # action 1: Ctrl-O, context
    read_until("Context", start)
    read_until("Pinned", start)
    read_until("Use 30 seconds.", start)
    read_until("Deploy only after the maintenance window.", start)
    write(b"\x12")  # action 2: Ctrl-R, reply to the selected question
    read_until("Replying to " + question, start)
    answer = "Use 30 seconds. Deploy only after the maintenance window."
    write(answer.encode())
    write(b"\r")  # action 3: submit
    read_until(answer, start)
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        accepted = [p for p in posts() if p["from"] == "me"]
        if accepted:
            break
        time.sleep(0.02)
    assert len(accepted) == 1 and accepted[0]["message"] == answer, accepted
    assert accepted[0]["reply_to"] == question, accepted
    peer = "PTY peer confirms the corrected timeout and deployment constraint."
    cli("send", "--reply-to", accepted[0]["id"], "--nick", "reviewer", "--message", peer)
    read_until(peer, start)
    print(json.dumps({"workflow": "conversation-effort-v1", "actions": 3,
                      "peer_observed": True, "scripted_seconds": time.monotonic() - began}))

    # A byte-by-byte multiline paste must remain a draft, including its Unicode.
    draft = "first é🐈\nsecond\ttail"
    start = len(received)
    write(b"\x1b[200~" + draft.encode() + b"\x1b[201~", split=True)
    read_until("first é🐈↵second⇥tail", start)
    assert len([p for p in posts() if p["from"] == "me"]) == 1, "paste submitted itself"
    write(b"\r")
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        accepted = [p for p in posts() if p["from"] == "me"]
        if len(accepted) == 2:
            break
        time.sleep(0.02)
    assert len(accepted) == 2 and accepted[-1]["message"] == draft, accepted

    # SDK validation rejects an oversized draft. Backspacing can recover it;
    # neither Enter nor the rejection may discard it or post any prefix.
    start = len(received)
    write(b"\x1b[200~" + b"z" * 4097 + b"\x1b[201~")
    read_until("z" * 50, start)
    write(b"\r")
    read_until("message exceeds 4096 bytes", start)
    assert len([p for p in posts() if p["from"] == "me"]) == 2
    start = len(received)
    write(b"\x7f\r")
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        accepted = [p for p in posts() if p["from"] == "me"]
        if len(accepted) == 3:
            break
        time.sleep(0.02)
    assert len(accepted) == 3 and accepted[-1]["message"] == "z" * 4096, "rejection lost the draft"
    write(b"\x03")
    process.wait(timeout=5)
    time.sleep(0.1)
    assert process.returncode == 0, process.returncode
    assert b"\x1b[?2004l" in received, "bracketed paste not disabled on exit"
    assert termios.tcgetattr(slave) == before, "terminal settings not restored"
    print(json.dumps({"paste_posts_before_enter": 0, "exact_multiline_body": True,
                      "validation_draft_recovered": True, "terminal_restored": True}))
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    reading.clear()
    reader.join(timeout=1)
    os.close(master)
    os.close(slave)
