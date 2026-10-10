/* Verify actual native key derivation from generated gateway/peer profiles.
 * SPDX-License-Identifier: BSD-2-Clause
 */
#include <coap3/coap_internal.h>
#include <coap3/coap_oscore_internal.h>
#include <oscore/oscore_context.h>
#include <stdio.h>
#include <string.h>

/* Caller owns the returned context and must call coap_free_context. */
static coap_context_t *load_context(const char *path) {
  uint8_t buffer[1024];
  FILE *file = NULL;
  coap_context_t *context = NULL;
  coap_oscore_conf_t *config = NULL;
  size_t length;
  file = fopen(path, "rb");
  if (file == NULL)
    goto cleanup;
  length = fread(buffer, 1, sizeof(buffer), file);
  if (ferror(file) || length == sizeof(buffer))
    goto cleanup;
  {
    const coap_str_const_t contents = {length, buffer};
    config = coap_new_oscore_conf(contents, NULL, NULL, 0);
  }
  if (config == NULL)
    goto cleanup;
  context = coap_new_context(NULL);
  if (context == NULL)
    goto cleanup;
  {
    int configured = coap_context_oscore_server(context, config);
    config = NULL; /* Consumed on success and failure by the pinned API. */
    if (!configured) {
      coap_free_context(context);
      context = NULL;
      goto cleanup;
    }
  }
cleanup:
  if (config != NULL)
    coap_delete_oscore_conf(config);
  if (file != NULL)
    fclose(file);
  memset(buffer, 0, sizeof(buffer));
  return context;
}

static int equal(const coap_bin_const_t *a, const coap_bin_const_t *b) {
  return a != NULL && b != NULL && a->length == b->length &&
         memcmp(a->s, b->s, a->length) == 0;
}

int main(int argc, char **argv) {
  coap_context_t *server = NULL, *peer = NULL;
  oscore_ctx_t *a, *b;
  int result = 1;
  if (argc != 3)
    return 2;
  coap_startup();
  server = load_context(argv[1]);
  peer = load_context(argv[2]);
  if (server == NULL || peer == NULL)
    goto cleanup;
  a = server->p_osc_ctx;
  b = peer->p_osc_ctx;
  if (a == NULL || b == NULL || a->recipient_chain == NULL || b->recipient_chain == NULL)
    goto cleanup;
  if (a->replay_window_size != 64 || b->replay_window_size != 64 ||
      a->ssn_freq != 8 || b->ssn_freq != 8 ||
      a->aead_alg != 10 || b->aead_alg != 10 ||
      a->hkdf_alg != -10 || b->hkdf_alg != -10 ||
      !a->rfc8613_b_1_2 || !b->rfc8613_b_1_2)
    goto cleanup;
  if (!equal(a->common_iv, b->common_iv) ||
      !equal(a->sender_context->sender_key, b->recipient_chain->recipient_key) ||
      !equal(b->sender_context->sender_key, a->recipient_chain->recipient_key) ||
      equal(a->sender_context->sender_key, b->sender_context->sender_key))
    goto cleanup;
  result = 0;
cleanup:
  coap_free_context(peer);
  coap_free_context(server);
  coap_cleanup();
  if (result != 0)
    fputs("native OSCORE context validation failed\n", stderr);
  return result;
}
