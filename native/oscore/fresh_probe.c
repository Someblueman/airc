/* Fresh Echo bootstrap control, not a gateway listener.
 * SPDX-License-Identifier: BSD-2-Clause */
#include "fresh_probe.h"
#include <coap3/coap_internal.h>
#include <coap3/coap_oscore_internal.h>
#include <arpa/inet.h>
#include <string.h>
#include <unistd.h>

static const uint8_t body[] = "fresh-echo";
static int accepted, answered;

static void receive(coap_resource_t *resource, coap_session_t *session,
                    const coap_pdu_t *request, const coap_string_t *query,
                    coap_pdu_t *response) {
  size_t length = 0;
  const uint8_t *data = NULL;
  (void)resource;
  (void)query;
  if (session->oscore_encryption && session->recipient_ctx != NULL &&
      !session->recipient_ctx->initial_state &&
      !session->recipient_ctx->persistence_failed &&
      coap_get_data(request, &length, &data) && length == sizeof(body)-1 &&
      memcmp(data, body, length) == 0) {
    ++accepted;
    coap_pdu_set_code(response, COAP_RESPONSE_CODE_CHANGED);
  } else {
    coap_pdu_set_code(response, COAP_RESPONSE_CODE_BAD_REQUEST);
  }
}

static coap_response_t response(coap_session_t *session, const coap_pdu_t *sent,
                                const coap_pdu_t *received, coap_mid_t mid) {
  (void)sent;
  (void)mid;
  if (session->oscore_encryption &&
      coap_pdu_get_code(received) == COAP_RESPONSE_CODE_CHANGED)
    ++answered;
  return COAP_RESPONSE_OK;
}

int airc_oscore_fresh_probe(coap_context_t *server) {
  uint8_t data[1024];
  ssize_t length;
  coap_address_t address;
  coap_endpoint_t *endpoint;
  coap_context_t *client = NULL;
  coap_session_t *session = NULL;
  coap_oscore_conf_t *configuration = NULL;
  coap_pdu_t *request = NULL;
  coap_resource_t *resource;
  int result = 1;
  accepted = answered = 0;
  coap_address_init(&address);
  address.size = sizeof(address.addr.sin);
  address.addr.sin.sin_family = AF_INET;
  address.addr.sin.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
  address.addr.sin.sin_port = 0;
  endpoint = coap_new_endpoint(server, &address, COAP_PROTO_UDP);
  if (endpoint == NULL)
    goto cleanup;
  resource = coap_resource_init(coap_make_str_const("rpc"), COAP_RESOURCE_FLAGS_OSCORE_ONLY);
  if (resource == NULL)
    goto cleanup;
  coap_register_handler(resource, COAP_REQUEST_POST, receive);
  coap_add_resource(server, resource);
  length = read(4, data, sizeof(data));
  if (length <= 0 || (size_t)length == sizeof(data))
    goto cleanup;
  {
    const coap_str_const_t contents = {(size_t)length, data};
    configuration = coap_new_oscore_conf(contents, NULL, NULL, 8);
  }
  if (configuration == NULL)
    goto cleanup;
  client = coap_new_context(NULL);
  if (client == NULL)
    goto cleanup;
  /* Libcoap retains the sent request for automatic encrypted Echo retry. */
  coap_context_set_block_mode(client, COAP_BLOCK_USE_LIBCOAP);
  coap_register_response_handler(client, response);
  session = coap_new_client_session3(client, NULL, &endpoint->bind_addr,
                                   COAP_PROTO_UDP, NULL, NULL, NULL);
  if (session == NULL)
    goto cleanup;
  {
    int configured;
    coap_lock_lock(goto cleanup);
    configured = coap_oscore_initiate(session, configuration);
    configuration = NULL; /* Initiation consumes config on success or failure. */
    coap_lock_unlock();
    if (!configured)
      goto cleanup;
  }
  request = coap_new_pdu(COAP_MESSAGE_CON, COAP_REQUEST_CODE_POST, session);
  if (request == NULL || !coap_add_token(request, 2, (const uint8_t *)"ft") ||
      !coap_add_option(request, COAP_OPTION_URI_PATH, 3, (const uint8_t *)"rpc") ||
      !coap_add_data(request, sizeof(body)-1, body))
    goto cleanup;
  {
    coap_mid_t mid = coap_send(session, request);
    request = NULL; /* Send always consumes the PDU. */
    if (mid == COAP_INVALID_MID)
      goto cleanup;
  }
  /* Both contexts run on the loading thread; callback I/O is independently bounded. */
  for (int i = 0; i < 200 && !answered; ++i) {
    if (coap_io_process(server, 10) < 0 || coap_io_process(client, 10) < 0)
      goto cleanup;
  }
  if (accepted == 1 && answered == 1)
    result = 0;
cleanup:
  coap_delete_pdu(request);
  if (session != NULL)
    coap_session_release(session);
  coap_free_context(client);
  if (configuration != NULL)
    coap_delete_oscore_conf(configuration);
  memset(data, 0, sizeof(data));
  return result;
}
