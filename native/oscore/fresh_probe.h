/* SPDX-License-Identifier: BSD-2-Clause */
#ifndef AIRC_OSCORE_FRESH_PROBE_H
#define AIRC_OSCORE_FRESH_PROBE_H
#include <coap3/coap.h>
/* Test-only native UDP exchange. Peer configuration is inherited on fd 4. */
int airc_oscore_fresh_probe(coap_context_t *server);
#endif
