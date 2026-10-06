/* SPDX-License-Identifier: BSD-2-Clause */
#ifndef AIRC_OSCORE_PERSISTENCE_H
#define AIRC_OSCORE_PERSISTENCE_H
#include <coap3/coap.h>
#include <stdint.h>
#include <pthread.h>

/* Caller owns the connected socket and this storage. Both must outlive all
 * native contexts that reference the callbacks. All crypto/context operations
 * must run on the thread that loads this state; callbacks reject other threads.
 * No callback closes the socket. */
typedef struct {
  int fd;
  int failed;
  uint8_t context_id[16];
  pthread_t owner;
} airc_oscore_persistence;

/* Reads bounded initialization from the owner process. Returns an allocated
 * config owned by the caller; coap_delete_oscore_conf frees it, or context setup
 * consumes it. No missing-state initialization or key generation occurs here. */
coap_oscore_conf_t *airc_oscore_load(airc_oscore_persistence *state, int fd);
int airc_oscore_receiver(const coap_session_t *session,
                        const coap_bin_const_t *recipient,
                        const coap_bin_const_t *context_id,
                        uint64_t sequence, uint64_t window);
#endif
