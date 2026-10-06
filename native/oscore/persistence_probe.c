/* Native callback integration control, not a gateway listener.
 * SPDX-License-Identifier: BSD-2-Clause */
#include "persistence.h"
#include <coap3/coap_internal.h>
#include <coap3/coap_oscore_internal.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

static const uint8_t plain[] = {0x42,0x01,0x00,0x01,0x11,0x22,0xb3,'r','p','c'};

static coap_session_t *session(coap_context_t *context, int server) {
  coap_session_t *result = coap_malloc_type(COAP_SESSION, sizeof(*result));
  if (result == NULL)
    return NULL;
  memset(result, 0, sizeof(*result));
  result->context = context;
  result->proto = COAP_PROTO_UDP;
  result->type = server ? COAP_SESSION_TYPE_SERVER : COAP_SESSION_TYPE_CLIENT;
  result->recipient_ctx = context->p_osc_ctx->recipient_chain;
  return result;
}

static coap_pdu_t *packet(const uint8_t *data, size_t length) {
  coap_pdu_t *result = coap_pdu_init(0, 0, 0, COAP_DEFAULT_MTU);
  if (result != NULL && !coap_pdu_parse(COAP_PROTO_UDP, data, length, result)) {
    coap_delete_pdu(result);
    return NULL;
  }
  return result;
}

static coap_context_t *peer_context(void) {
  uint8_t data[1024];
  ssize_t length = read(4, data, sizeof(data)); /* Test-only regular config file. */
  coap_context_t *context = NULL;
  coap_oscore_conf_t *configuration = NULL;
  if (length <= 0 || (size_t)length == sizeof(data))
    goto cleanup;
  {
    const coap_str_const_t contents = {(size_t)length, data};
    configuration = coap_new_oscore_conf(contents, NULL, NULL, 8);
  }
  if (configuration == NULL)
    goto cleanup;
  context = coap_new_context(NULL);
  if (context == NULL)
    goto cleanup;
  {
    int configured = coap_context_oscore_server(context, configuration);
    configuration = NULL;
    if (!configured) {
      coap_free_context(context);
      context = NULL;
    }
  }
cleanup:
  if (configuration != NULL)
    coap_delete_oscore_conf(configuration);
  memset(data, 0, sizeof(data));
  return context;
}

int main(int argc, char **argv) {
  airc_oscore_persistence state;
  coap_context_t *context = NULL, *peer = NULL;
  coap_session_t *local = NULL, *remote = NULL;
  coap_oscore_conf_t *configuration = NULL;
  coap_pdu_t *input = NULL, *encrypted = NULL, *incoming = NULL, *output = NULL;
  int result = 1, sender_mode, expect_success, configured;
  uint64_t start_sequence = 0;
  if (argc != 2)
    return 2;
  sender_mode = strncmp(argv[1], "sender-", 7) == 0;
  expect_success = strcmp(argv[1], "sender-ok") == 0 || strcmp(argv[1], "receiver-ok") == 0;
  coap_startup();
  configuration = airc_oscore_load(&state, 3);
  if (configuration == NULL)
    goto cleanup;
  start_sequence = configuration->start_seq_num;
  context = coap_new_context(NULL);
  if (context == NULL)
    goto cleanup;
  configured = coap_context_oscore_server(context, configuration);
  configuration = NULL;
  if (!configured)
    goto cleanup;
  coap_context_set_app_data2(context, &state, NULL);
  coap_oscore_register_external_handlers(context, NULL, airc_oscore_receiver);
  local = session(context, !sender_mode);
  input = packet(plain, sizeof(plain));
  if (local == NULL || input == NULL)
    goto cleanup;
  {
    char resume = 0;
    if (write(5, "R", 1) != 1 || read(5, &resume, 1) != 1 || resume != 'G')
      goto cleanup;
  }
  if (sender_mode) {
    encrypted = coap_oscore_new_pdu_encrypted(local, input, NULL, OSCORE_SEND_PARTIAL_IV);
    if ((encrypted != NULL) != expect_success)
      goto cleanup;
    if (expect_success) {
      coap_opt_iterator_t iterator;
      coap_opt_t *option = coap_check_option(encrypted, COAP_OPTION_OSCORE, &iterator);
      uint64_t partial_iv = 0;
      size_t length;
      const uint8_t *value;
      if (option == NULL)
        goto cleanup;
      length = coap_opt_length(option);
      value = coap_opt_value(option);
      if (length == 0 || (value[0]&7) == 0 || (value[0]&7) > 5 || length < (size_t)(value[0]&7)+1)
        goto cleanup;
      for (size_t i = 0; i < (value[0]&7); ++i)
        partial_iv = (partial_iv<<8) | value[i+1];
      if (partial_iv != start_sequence)
        goto cleanup;
      printf("nonce=%llu\n", (unsigned long long)partial_iv);
    }
    if (!expect_success) {
      encrypted = coap_oscore_new_pdu_encrypted(local, input, NULL, OSCORE_SEND_PARTIAL_IV);
      if (encrypted != NULL)
        goto cleanup;
    }
  } else {
    peer = peer_context();
    if (peer == NULL)
      goto cleanup;
    remote = session(peer, 0);
    if (remote == NULL)
      goto cleanup;
    encrypted = coap_oscore_new_pdu_encrypted(remote, input, NULL, OSCORE_SEND_PARTIAL_IV);
    if (encrypted == NULL || !coap_pdu_encode_header(encrypted, COAP_PROTO_UDP))
      goto cleanup;
    if (strcmp(argv[1], "receiver-tamper") == 0)
      encrypted->token[encrypted->used_size-1] ^= 1;
    incoming = packet(encrypted->token-encrypted->hdr_size, encrypted->hdr_size+encrypted->used_size);
    if (incoming == NULL)
      goto cleanup;
    incoming->mid += strcmp(argv[1], "receiver-replay") == 0 ? 2 : 1;
    /* Replayed ciphertext gets a different outer MID from the accepted control. */
    if (strcmp(argv[1], "receiver-tamper") == 0)
      incoming->type = COAP_MESSAGE_NON; /* No synthetic network ACK on failure. */
    coap_lock_lock(goto cleanup);
    output = coap_oscore_decrypt_pdu(local, incoming);
    coap_lock_unlock();
    if ((output != NULL) != expect_success)
      goto cleanup;
    if (strcmp(argv[1], "receiver-fail") == 0) {
      coap_lock_lock(goto cleanup);
      output = coap_oscore_decrypt_pdu(local, incoming);
      coap_lock_unlock();
      if (output != NULL)
        goto cleanup;
    }
  }
  result = 0;
cleanup:
  coap_delete_pdu(output);
  coap_delete_pdu(incoming);
  coap_delete_pdu(encrypted);
  coap_delete_pdu(input);
  if (remote != NULL) {
    oscore_delete_server_associations(remote);
    coap_free(remote);
  }
  if (local != NULL) {
    oscore_delete_server_associations(local);
    coap_free(local);
  }
  coap_free_context(peer);
  coap_free_context(context);
  if (configuration != NULL)
    coap_delete_oscore_conf(configuration);
  close(3);
  close(4);
  close(5);
  coap_cleanup();
  if (result != 0)
    fputs("native OSCORE persistence probe failed\n", stderr);
  return result;
}
