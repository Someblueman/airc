/* SPDX-License-Identifier: BSD-2-Clause */
#include "worker.h"
#include "ipc.h"
#include <coap3/coap_internal.h>
#include <coap3/coap_oscore_internal.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
  airc_oscore_worker *owner;
  coap_tick_t started;
  size_t length;
  uint8_t data[];
} response_body;

static void release_response(coap_session_t *session, void *parameter) {
  response_body *body = parameter;
  (void)session;
  --body->owner->responses;
  memset(body->data, 0, body->length);
  free(body);
}

void airc_oscore_rpc(coap_resource_t *resource, coap_session_t *session,
                    const coap_pdu_t *request, const coap_string_t *query,
                    coap_pdu_t *response) {
  airc_oscore_worker *worker = coap_resource_get_userdata(resource);
  const uint8_t *data;
  size_t length, offset, total;
  coap_opt_iterator_t iterator;
  coap_opt_t *format;
  uint8_t header[4], reply[5];
  uint32_t reply_length;
  int64_t now;
  response_body *body;
  coap_pdu_set_code(response, COAP_RESPONSE_CODE_BAD_REQUEST);
  if (!session->oscore_encryption || session->recipient_ctx == NULL ||
      session->recipient_ctx->initial_state || session->recipient_ctx->persistence_failed ||
      coap_check_option(request, COAP_OPTION_OBSERVE, &iterator) != NULL ||
      !coap_get_data_large(request, &length, &data, &offset, &total) ||
      length == 0 || length > 16384 || offset != 0 || total != length)
    return;
  format = coap_check_option(request, COAP_OPTION_CONTENT_FORMAT, &iterator);
  if (format == NULL || coap_opt_length(format) > 2 || coap_decode_var_bytes(coap_opt_value(format), coap_opt_length(format)) != COAP_MEDIATYPE_APPLICATION_JSON) {
    coap_pdu_set_code(response, COAP_RESPONSE_CODE_UNSUPPORTED_CONTENT_FORMAT);
    return;
  }
  if (worker->responses >= 4) {
    coap_pdu_set_code(response, COAP_RESPONSE_CODE_SERVICE_UNAVAILABLE);
    return;
  }
  now = airc_oscore_now();
  if (now < 0)
    goto failed;
  for (size_t i = 0; i < 4; ++i)
    header[i] = (uint8_t)(length >> (8*(3-i)));
  if (!airc_oscore_io(4, header, sizeof(header), 1, now+3000) ||
      !airc_oscore_io(4, (uint8_t *)data, length, 1, now+3000) ||
      !airc_oscore_io(4, reply, sizeof(reply), 0, now+10000))
    goto failed;
  reply_length = ((uint32_t)reply[1]<<24) | ((uint32_t)reply[2]<<16) |
                 ((uint32_t)reply[3]<<8) | reply[4];
  if (reply_length == 0 || reply_length > 65536 ||
      ((reply[0]>>5) != 2 && (reply[0]>>5) != 4 && (reply[0]>>5) != 5))
    goto failed;
  body = malloc(sizeof(*body)+reply_length);
  if (body == NULL)
    goto failed;
  body->owner = worker;
  body->length = reply_length;
  coap_ticks(&body->started);
  ++worker->responses;
  if (!airc_oscore_io(4, body->data, reply_length, 0, now+10000)) {
    release_response(session, body);
    goto failed;
  }
  coap_pdu_set_code(response, (coap_pdu_code_t)reply[0]);
  /* Pinned API always consumes the release callback on success or failure. */
  (void)coap_add_data_large_response(resource, session, request, response, query,
                                    COAP_MEDIATYPE_APPLICATION_JSON, 0, 0,
                                    body->length, body->data, release_response, body);
  return;
failed:
  worker->rpc_failed = 1;
  coap_pdu_set_code(response, COAP_RESPONSE_CODE_SERVICE_UNAVAILABLE);
}

int airc_oscore_expire(coap_context_t *context) {
  coap_endpoint_t *endpoint;
  coap_session_t *session, *temporary;
  coap_tick_t now;
  coap_lock_lock(return 0);
  coap_ticks(&now);
  LL_FOREACH(context->endpoint, endpoint) {
    SESSIONS_ITER(endpoint->sessions, session, temporary) {
      coap_lg_srcv_t *receive, *next_receive;
      coap_lg_xmit_t *send, *next_send;
      LL_FOREACH_SAFE(session->lg_srcv, receive, next_receive) {
        if (!receive->ref && !receive->dont_timeout && now-receive->airc_started >= 3*COAP_TICKS_PER_SECOND) {
          LL_DELETE(session->lg_srcv, receive);
          coap_block_delete_lg_srcv(session, receive);
        }
      }
      LL_FOREACH_SAFE(session->lg_xmit, send, next_send) {
        if (!send->ref && send->data_info != NULL && send->data_info->release_func == release_response) {
          response_body *body = send->data_info->app_ptr;
          if (now-body->started >= 3*COAP_TICKS_PER_SECOND) {
            LL_DELETE(session->lg_xmit, send);
            coap_block_delete_lg_xmit(session, send);
          }
        }
      }
    }
  }
  coap_lock_unlock();
  return 1;
}
