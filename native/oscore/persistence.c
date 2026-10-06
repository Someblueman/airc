/* SPDX-License-Identifier: BSD-2-Clause */
#include "persistence.h"
#include <coap3/coap_internal.h>
#include <coap3/coap_oscore_internal.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

static int64_t milliseconds(void) {
  struct timespec now;
  if (clock_gettime(CLOCK_MONOTONIC, &now) != 0)
    return -1;
  return (int64_t)now.tv_sec * 1000 + now.tv_nsec / 1000000;
}

static int transfer(int fd, uint8_t *bytes, size_t length, int write_data,
                    int64_t deadline) {
  while (length != 0) {
    int64_t now = milliseconds();
    struct pollfd readiness = {fd, write_data ? POLLOUT : POLLIN, 0};
    ssize_t count;
    int ready;
    if (now < 0 || now >= deadline)
      return 0;
    ready = poll(&readiness, 1, (int)(deadline-now));
    if (ready < 0 && errno == EINTR)
      continue;
    if (ready <= 0)
      return 0;
    if (write_data) {
#ifdef MSG_NOSIGNAL
      count = send(fd, bytes, length, MSG_NOSIGNAL);
#else
      count = send(fd, bytes, length, 0);
#endif
    } else {
      count = recv(fd, bytes, length, 0);
    }
    if (count < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK))
      continue;
    if (count <= 0)
      return 0;
    bytes += count;
    length -= (size_t)count;
  }
  return 1;
}

static uint64_t decode(const uint8_t *bytes) {
  uint64_t value = 0;
  for (size_t i = 0; i < 8; ++i)
    value = (value << 8) | bytes[i];
  return value;
}
static void encode(uint8_t *bytes, uint64_t value) {
  for (size_t i = 8; i != 0; --i) {
    bytes[i-1] = (uint8_t)value;
    value >>= 8;
  }
}

static int save(airc_oscore_persistence *state, uint8_t kind,
                uint64_t sequence, uint64_t window) {
  uint8_t request[17], ack = 0;
  int64_t now = milliseconds();
  if (state == NULL || !pthread_equal(state->owner, pthread_self()))
    return 0;
  if (state->failed || now < 0)
    return 0;
  request[0] = kind;
  encode(request+1, sequence);
  encode(request+9, window);
  if (!transfer(state->fd, request, sizeof(request), 1, now+3000) ||
      !transfer(state->fd, &ack, 1, 0, now+3000) || ack != 1) {
    state->failed = 1;
    return 0;
  }
  return 1;
}
static int sender(uint64_t sequence, void *parameter) {
  return save(parameter, 1, sequence, 0);
}

coap_oscore_conf_t *airc_oscore_load(airc_oscore_persistence *state, int fd) {
  uint8_t header[34], data[1024];
  coap_oscore_conf_t *configuration = NULL;
  uint32_t length;
  uint64_t sender_end, sequence, window;
  int64_t now = milliseconds();
  memset(state, 0, sizeof(*state));
  state->fd = fd;
  state->owner = pthread_self();
  state->failed = 1;
  {
    int flags = fcntl(fd, F_GETFL);
    if (flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) != 0)
      goto cleanup;
  }
#ifdef SO_NOSIGPIPE
  {
    int enabled = 1;
    if (setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &enabled, sizeof(enabled)) != 0)
      goto cleanup;
  }
#endif
  if (now < 0 || !transfer(fd, header, sizeof(header), 0, now+3000) ||
      memcmp(header, "AGOS1", 5) != 0)
    goto cleanup;
  length = ((uint32_t)header[5]<<24) | ((uint32_t)header[6]<<16) |
           ((uint32_t)header[7]<<8) | header[8];
  sender_end = decode(header+9);
  sequence = decode(header+18);
  window = decode(header+26);
  if (length == 0 || length > sizeof(data) || header[17] > 1 ||
      sender_end >= ((UINT64_C(1)<<40)-1) || sequence >= ((UINT64_C(1)<<40)-1) ||
      (header[17] && !(window&1)) || (!header[17] && (sequence || window)) ||
      (header[17] && sequence < 63 && (window>>(sequence+1)) != 0) ||
      !transfer(fd, data, length, 0, now+3000))
    goto cleanup;
  {
    const coap_str_const_t contents = {length, data};
    configuration = coap_new_oscore_conf(contents, sender, state, sender_end);
  }
  if (configuration == NULL)
    goto cleanup;
  if (configuration->id_context == NULL || configuration->id_context->length != 16 ||
      configuration->master_secret == NULL || configuration->master_secret->length != 32 ||
      configuration->master_salt == NULL || configuration->master_salt->length != 16 ||
      configuration->sender == NULL || configuration->sender->sender_id == NULL ||
      configuration->sender->sender_id->length != 1 || configuration->sender->sender_id->s[0] != 1 ||
      configuration->recipient_chain == NULL ||
      configuration->recipient_chain->next_recipient != NULL ||
      configuration->recipient_chain->recipient_id == NULL ||
      configuration->recipient_chain->recipient_id->length != 1 ||
      configuration->recipient_chain->recipient_id->s[0] != 2 ||
      configuration->replay_window != 64 || configuration->ssn_freq != 8 ||
      configuration->aead_alg != 10 || configuration->hkdf_alg != -10 ||
      !configuration->rfc8613_b_1_2 || configuration->rfc8613_b_2)
    goto invalid;
  memcpy(state->context_id, configuration->id_context->s, 16);
  if (header[17] && !coap_oscore_recipient_set_latest_seq(configuration,
      configuration->recipient_chain->recipient_id, sequence, window))
    goto invalid;
  state->failed = 0;
  goto cleanup;
invalid:
  coap_delete_oscore_conf(configuration);
  configuration = NULL;
cleanup:
  memset(data, 0, sizeof(data));
  return configuration;
}

int airc_oscore_receiver(const coap_session_t *session,
                        const coap_bin_const_t *recipient,
                        const coap_bin_const_t *context_id,
                        uint64_t sequence, uint64_t window) {
  airc_oscore_persistence *state = coap_context_get_app_data(coap_session_get_context(session));
  if (state == NULL || recipient == NULL || recipient->length != 1 ||
      recipient->s[0] != 2 || context_id == NULL || context_id->length != 16 ||
      memcmp(context_id->s, state->context_id, 16) != 0)
    return 0;
  return save(state, 2, sequence, window);
}

void airc_oscore_configure(coap_context_t *context, airc_oscore_persistence *state) {
  context->oscore_max_sessions = 16;
  context->oscore_max_associations = 256;
  coap_context_set_max_idle_sessions(context, 16);
  coap_context_set_max_token_size(context, 8);
  coap_context_set_max_body_size(context, 64 * 1024);
  coap_context_set_max_bodies_ram(context, 256 * 1024);
  coap_context_set_app_data2(context, state, NULL);
  coap_oscore_register_external_handlers(context, NULL, airc_oscore_receiver);
}
