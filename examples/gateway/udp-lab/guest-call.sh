#!/usr/bin/env bash
# On the VPS, submit a saved encrypted envelope as the restricted guest.
set -euo pipefail
exec /usr/local/lib/airc-lab/udp-client \
    --address 127.0.0.1:9002 --bind 127.0.0.1:49153 \
    --signing-public "$HOME/airc-client/gateway-signing.pem" \
    --recipient-private "$HOME/airc-client/recipient.pem" \
    --realm vps-udp-lab --principal guest --binding guest-native \
    --route guest-return --signing-id gateway-signing --recipient-id guest-recipient
