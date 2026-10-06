/* SPDX-License-Identifier: BSD-2-Clause
 * RFC 8613 C.4 request vectors also appear in libcoap tests/test_oscore.c.
 * See LICENSE.libcoap for upstream attribution and terms.
 */
#include <coap3/coap_internal.h>
#include <coap3/coap_oscore_internal.h>
#include <oscore/oscore.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CHECK(x) do { if (!(x)) { \
  fprintf(stderr, "failed at line %d: %s\n", __LINE__, #x); \
  exit(EXIT_FAILURE); \
} } while (0)

static const uint8_t plain[] = {
  0x44,0x01,0x5d,0x1f,0,0,0x39,0x74,0x39,0x6c,0x6f,0x63,
  0x61,0x6c,0x68,0x6f,0x73,0x74,0x83,0x74,0x76,0x31
};
static const uint8_t encrypted[] = {
  0x44,0x02,0x5d,0x1f,0,0,0x39,0x74,0x39,0x6c,0x6f,0x63,
  0x61,0x6c,0x68,0x6f,0x73,0x74,0x62,0x09,0x14,0xff,0x61,
  0x2f,0x10,0x92,0xf1,0x77,0x6f,0x1c,0x16,0x68,0xb3,0x82,0x5e
};
static const char client_config[] =
  "master_secret,hex,\"0102030405060708090a0b0c0d0e0f10\"\n"
  "master_salt,hex,\"9e7ca92223786340\"\n"
  "sender_id,hex,\"\"\nrecipient_id,hex,\"01\"\n";
static const char server_config[] =
  "master_secret,hex,\"0102030405060708090a0b0c0d0e0f10\"\n"
  "master_salt,hex,\"9e7ca92223786340\"\n"
  "sender_id,hex,\"01\"\nrecipient_id,hex,\"\"\n";
static unsigned saves;
static int save_ok;
static uint64_t saved_seq, saved_window;

static int save_sender(uint64_t seq, void *unused) {
  (void)unused;
  ++saves;
  saved_seq = seq;
  return save_ok;
}
static int save_receiver(const coap_session_t *session,
                         const coap_bin_const_t *recipient,
                         const coap_bin_const_t *context,
                         uint64_t seq, uint64_t window) {
  (void)session;
  (void)recipient;
  (void)context;
  ++saves;
  saved_seq = seq;
  saved_window = window;
  return save_ok;
}

/* This fixture owns the context and synthetic session, as upstream unit tests
 * do. No I/O loop or application dispatch runs; callers own returned PDUs. */
typedef struct {
  coap_context_t *context;
  coap_session_t *session;
} fixture;

static fixture create_fixture(int server, uint64_t start) {
  fixture f = {NULL, NULL};
  const char *text = server ? server_config : client_config;
  coap_str_const_t config = {strlen(text), (const uint8_t *)text};
  coap_bin_const_t recipient = {0, NULL};
  coap_oscore_conf_t *conf;
  f.context = coap_new_context(NULL);
  CHECK(f.context != NULL);
  conf = coap_new_oscore_conf(config, server ? NULL : save_sender, NULL, start);
  CHECK(conf != NULL);
  if (server) {
    CHECK(!coap_oscore_recipient_set_latest_seq(conf, &recipient, OSCORE_SEQ_MAX, 1));
    CHECK(!coap_oscore_recipient_set_latest_seq(conf, &recipient, start, 0));
    CHECK(coap_oscore_recipient_set_latest_seq(conf, &recipient, start, 1));
  }
  CHECK(coap_context_oscore_server(f.context, conf)); /* transfers conf */
  f.session = coap_malloc_type(COAP_SESSION, sizeof(*f.session));
  CHECK(f.session != NULL);
  memset(f.session, 0, sizeof(*f.session));
  f.session->context = f.context;
  f.session->proto = COAP_PROTO_UDP;
  f.session->type = server ? COAP_SESSION_TYPE_SERVER : COAP_SESSION_TYPE_CLIENT;
  f.session->recipient_ctx = f.context->p_osc_ctx->recipient_chain;
  if (server) {
    CHECK(f.session->recipient_ctx->initial_state == 0);
    coap_oscore_register_external_handlers(f.context, NULL, save_receiver);
  }
  saves = 0;
  save_ok = 1;
  return f;
}
static void destroy_fixture(fixture *f) {
  oscore_delete_server_associations(f->session);
  coap_free(f->session);
  coap_free_context(f->context);
}

/* Allocates a PDU owned by the caller; release with coap_delete_pdu. */
static coap_pdu_t *parse(const uint8_t *data, size_t length) {
  coap_pdu_t *pdu = coap_pdu_init(0, 0, 0, COAP_DEFAULT_MTU);
  CHECK(pdu != NULL);
  CHECK(coap_pdu_parse(COAP_PROTO_UDP, data, length, pdu));
  return pdu;
}

/* Decryption is an internal entry point called under libcoap's global lock. */
static coap_pdu_t *decrypt(fixture *f, coap_pdu_t *pdu) {
  coap_pdu_t *out;
  coap_lock_lock(return NULL);
  out = coap_oscore_decrypt_pdu(f->session, pdu);
  coap_lock_unlock();
  return out;
}

static void check_sender(void) {
  fixture f = create_fixture(0, 20);
  coap_pdu_t *pdu = parse(plain, sizeof(plain));
  coap_pdu_t *out = coap_oscore_new_pdu_encrypted(f.session, pdu, NULL, 0);
  CHECK(out != NULL);
  CHECK(saves == 1 && saved_seq == 21);
  CHECK(coap_pdu_encode_header(out, COAP_PROTO_UDP));
  CHECK(out->hdr_size + out->used_size == sizeof(encrypted));
  CHECK(memcmp(out->token - out->hdr_size, encrypted, sizeof(encrypted)) == 0);
  coap_delete_pdu(out);
  coap_delete_pdu(pdu);
  destroy_fixture(&f);

  f = create_fixture(0, 20);
  save_ok = 0;
  pdu = parse(plain, sizeof(plain));
  CHECK(coap_oscore_new_pdu_encrypted(f.session, pdu, NULL, 0) == NULL);
  CHECK(saves == 1);
  save_ok = 1; /* Restoring storage does not unpoison this context. */
  CHECK(coap_oscore_new_pdu_encrypted(f.session, pdu, NULL, 0) == NULL);
  CHECK(saves == 1);
  coap_delete_pdu(pdu);
  destroy_fixture(&f);
}

static void check_receiver(void) {
  fixture f = create_fixture(1, 19);
  uint8_t tampered[sizeof(encrypted)];
  coap_pdu_t *pdu, *out;
  memcpy(tampered, encrypted, sizeof(tampered));
  tampered[sizeof(tampered)-1] ^= 1;
  pdu = parse(tampered, sizeof(tampered));
  CHECK(decrypt(&f, pdu) == NULL);
  CHECK(saves == 0 && f.session->recipient_ctx->last_seq == 19);
  coap_delete_pdu(pdu);
  pdu = parse(encrypted, sizeof(encrypted));
  out = decrypt(&f, pdu);
  CHECK(out != NULL && saves == 1 && saved_seq == 20 && saved_window == 3);
  CHECK(coap_pdu_encode_header(out, COAP_PROTO_UDP));
  CHECK(out->hdr_size + out->used_size == sizeof(plain));
  CHECK(memcmp(out->token - out->hdr_size, plain, sizeof(plain)) == 0);
  coap_delete_pdu(out);
  coap_delete_pdu(pdu);
  destroy_fixture(&f);

  f = create_fixture(1, 20); /* Simulates loading authenticated persisted state. */
  pdu = parse(encrypted, sizeof(encrypted));
  pdu->mid++; /* Fresh outer MID does not bypass OSCORE replay protection. */
  CHECK(decrypt(&f, pdu) == NULL && saves == 0);
  coap_delete_pdu(pdu);
  destroy_fixture(&f);

  f = create_fixture(1, 19);
  save_ok = 0;
  pdu = parse(encrypted, sizeof(encrypted));
  CHECK(decrypt(&f, pdu) == NULL && saves == 1);
  CHECK(f.session->recipient_ctx->last_seq == 19);
  coap_delete_pdu(pdu);
  save_ok = 1;
  pdu = parse(encrypted, sizeof(encrypted));
  CHECK(decrypt(&f, pdu) == NULL && saves == 1);
  coap_delete_pdu(pdu);
  destroy_fixture(&f);
}

static void check_zero_rollback(void) {
  fixture restored = create_fixture(1, 0);
  oscore_recipient_ctx_t recipient;
  cose_encrypt0_t cose;
  cose_encrypt0_init(&cose);
  CHECK(!oscore_validate_sender_seq(restored.session->recipient_ctx, &cose));
  destroy_fixture(&restored);
  memset(&recipient, 0, sizeof(recipient));
  recipient.initial_state = 1;
  cose_encrypt0_init(&cose);
  CHECK(oscore_validate_sender_seq(&recipient, &cose));
  CHECK(recipient.initial_state == 0 && recipient.sliding_window == 1);
  oscore_roll_back_seq(&recipient);
  CHECK(recipient.initial_state == 1 && recipient.sliding_window == 0 && recipient.last_seq == 0);
}

int main(void) {
  coap_startup();
  check_sender();
  check_receiver();
  check_zero_rollback();
  coap_cleanup();
  puts("OSCORE persistence boundaries passed");
  return EXIT_SUCCESS;
}
