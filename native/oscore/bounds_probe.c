/* Exercises the real native allocation boundaries, not a network acceptance test.
 * SPDX-License-Identifier: BSD-2-Clause */
#include "bounds_probe.h"
#include <coap3/coap_internal.h>
#include <coap3/coap_oscore_internal.h>
#include <arpa/inet.h>
#include <stdio.h>
#include <string.h>

#define CHECK(x) do { if (!(x)) { \
  fprintf(stderr, "native bounds check failed at line %d: %s\n", __LINE__, #x); \
  goto cleanup; \
} } while (0)

int airc_oscore_bounds_probe(coap_context_t *context) {
  coap_address_t address;
  coap_packet_t packet;
  coap_session_t *sessions[16] = {NULL};
  coap_endpoint_t *endpoint;
  uint8_t bytes[2];
  coap_bin_const_t token = {sizeof(bytes), bytes};
  oscore_association_t *association;
  int locked = 0, result = 1;
  CHECK(context->oscore_max_sessions == 16 && context->oscore_max_associations == 256);
  CHECK(context->max_idle_sessions == 16 && context->max_token_size == 8);
  CHECK(context->max_body_size == 64 * 1024 && context->max_bodies_ram == 256 * 1024);
  coap_address_init(&address);
  address.size = sizeof(address.addr.sin);
  address.addr.sin.sin_family = AF_INET;
  address.addr.sin.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
  address.addr.sin.sin_port = 0;
  endpoint = coap_new_endpoint(context, &address, COAP_PROTO_UDP);
  CHECK(endpoint != NULL);
  memset(&packet, 0, sizeof(packet));
  packet.addr_info.local = endpoint->bind_addr;
  packet.addr_info.remote = endpoint->bind_addr;
  coap_lock_lock(goto cleanup);
  locked = 1;
  for (unsigned int i = 0; i < 16; ++i) {
    packet.addr_info.remote.addr.sin.sin_port = htons((uint16_t)(10000+i));
    sessions[i] = coap_endpoint_get_session(endpoint, &packet, i);
    CHECK(sessions[i] != NULL);
    coap_session_reference_lkd(sessions[i]); /* Simulate active transfers. */
  }
  packet.addr_info.remote.addr.sin.sin_port = htons(10016);
  CHECK(coap_endpoint_get_session(endpoint, &packet, 17) == NULL);
  packet.addr_info.remote.addr.sin.sin_port = htons(10015);
  CHECK(coap_endpoint_get_session(endpoint, &packet, 18) == sessions[15]);
  coap_session_release_lkd(sessions[0]);
  sessions[0] = NULL; /* Now idle: admission may evict it to recover capacity. */
  packet.addr_info.remote.addr.sin.sin_port = htons(10016);
  sessions[0] = coap_endpoint_get_session(endpoint, &packet, 19);
  CHECK(sessions[0] != NULL);
  coap_session_reference_lkd(sessions[0]);
  for (unsigned int i = 0; i < 256; ++i) {
    bytes[0] = (uint8_t)(i>>8);
    bytes[1] = (uint8_t)i;
    CHECK(oscore_new_association(sessions[0], NULL, &token,
                                context->p_osc_ctx->recipient_chain,
                                NULL, NULL, NULL, 0));
  }
  bytes[0] = 1;
  bytes[1] = 0;
  CHECK(!oscore_new_association(sessions[0], NULL, &token,
                               context->p_osc_ctx->recipient_chain,
                               NULL, NULL, NULL, 0));
  CHECK(oscore_find_association(sessions[0], &token) == NULL);
  for (unsigned int i = 0; i < 256; ++i) {
    bytes[0] = (uint8_t)(i>>8);
    bytes[1] = (uint8_t)i;
    CHECK(oscore_find_association(sessions[0], &token) != NULL);
  }
  bytes[0] = bytes[1] = 0;
  association = oscore_find_association(sessions[0], &token);
  CHECK(association != NULL && oscore_delete_association(sessions[0], association));
  bytes[0] = 1;
  CHECK(oscore_new_association(sessions[0], NULL, &token,
                              context->p_osc_ctx->recipient_chain,
                              NULL, NULL, NULL, 0));
  CHECK(oscore_find_association(sessions[0], &token) != NULL);
  result = 0;
cleanup:
  if (locked) {
    for (unsigned int i = 0; i < 16; ++i) {
      if (sessions[i] != NULL) {
        coap_session_release_lkd(sessions[i]);
        coap_session_free(sessions[i]);
      }
    }
    coap_lock_unlock();
  }
  return result;
}
