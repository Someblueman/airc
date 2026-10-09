#!/usr/bin/env bash
# On the VPS, submit plain JSON as any account; no client keys are needed.
set -euo pipefail
exec /usr/local/lib/airc-lab/udp-anonymous-client --address 127.0.0.1:9002 "$@"
