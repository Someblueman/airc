#!/usr/bin/env bash
# Send an already prepared envelope. Reuse the file when retrying a send.
set -euo pipefail
if [ "$#" -ne 2 ]; then
    printf '%s\n' 'usage: call.sh GATEWAY_IP:PORT LOCAL_IP:PORT < request.jwe' >&2
    exit 2
fi
repo=$(cd -- "$(dirname -- "$0")/../../.." && pwd)
lab="${AIRC_LAB_DIR:-$repo/bin/vps-lab}"
client="${AIRC_UDP_CLIENT:-$repo/bin/vps-lab-build/udp-client}"
exec "$client" \
    --address "$1" --bind "$2" \
    --signing-public "$lab/client/gateway-signing.pem" \
    --recipient-private "$lab/client/recipient.pem" \
    --realm vps-udp-lab --principal agent --binding native \
    --route agent-return --signing-id gateway-signing --recipient-id agent-recipient
