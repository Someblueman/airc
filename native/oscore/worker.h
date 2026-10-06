/* SPDX-License-Identifier: BSD-2-Clause */
#ifndef AIRC_OSCORE_WORKER_H
#define AIRC_OSCORE_WORKER_H
#include "persistence.h"

typedef struct {
  airc_oscore_persistence persistence;
  int rpc_failed;
  unsigned int responses;
} airc_oscore_worker;

void airc_oscore_rpc(coap_resource_t *resource, coap_session_t *session,
                    const coap_pdu_t *request, const coap_string_t *query,
                    coap_pdu_t *response);
/* Run on the owner thread before reading additional network packets. */
int airc_oscore_expire(coap_context_t *context);
#endif
