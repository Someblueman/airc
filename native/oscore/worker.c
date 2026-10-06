/* SPDX-License-Identifier: BSD-2-Clause */
#include "worker.h"
#include "ipc.h"
#include <coap3/coap_internal.h>
#include <coap3/coap_oscore_internal.h>
#include <arpa/inet.h>
#include <errno.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

static volatile sig_atomic_t stopped;
static void stop(int signal_number) { (void)signal_number; stopped = 1; }

static int owner_gone(void) {
  struct pollfd sockets[2] = {{3, POLLIN, 0}, {4, POLLIN, 0}};
  if (poll(sockets, 2, 0) < 0)
    return errno != EINTR;
  for (size_t i = 0; i < 2; ++i)
    if (sockets[i].revents & (POLLHUP | POLLERR | POLLNVAL | POLLIN))
      return 1; /* No unsolicited owner messages are valid between callbacks. */
  return 0;
}

int main(int argc, char **argv) {
  airc_oscore_worker worker = {0};
  coap_context_t *context = NULL;
  coap_oscore_conf_t *configuration = NULL;
  coap_address_t address;
  coap_endpoint_t *endpoint;
  coap_resource_t *resource;
  char *end;
  unsigned long port;
  uint8_t ready[6] = {'A','G','O','1',0,0};
  int configured, result = 1;
  int64_t now;
  struct sigaction action = {0};
  if (argc != 3)
    return 2;
  errno = 0;
  port = strtoul(argv[2], &end, 10);
  if (errno || *argv[2] == 0 || *end != 0 || port > 65535)
    return 2;
  coap_address_init(&address);
  if (inet_pton(AF_INET, argv[1], &address.addr.sin.sin_addr) == 1) {
    address.size = sizeof(address.addr.sin);
    address.addr.sin.sin_family = AF_INET;
    address.addr.sin.sin_port = htons((uint16_t)port);
  } else if (inet_pton(AF_INET6, argv[1], &address.addr.sin6.sin6_addr) == 1) {
    address.size = sizeof(address.addr.sin6);
    address.addr.sin6.sin6_family = AF_INET6;
    address.addr.sin6.sin6_port = htons((uint16_t)port);
  } else {
    return 2;
  }
  action.sa_handler = stop;
  if (sigaction(SIGINT, &action, NULL) != 0 || sigaction(SIGTERM, &action, NULL) != 0)
    return 1;
  coap_startup();
  coap_set_log_level(COAP_LOG_EMERG);
  if (!airc_oscore_socket(4))
    goto cleanup;
  configuration = airc_oscore_load(&worker.persistence, 3);
  if (configuration == NULL)
    goto cleanup;
  context = coap_new_context(NULL);
  if (context == NULL)
    goto cleanup;
  configured = coap_context_oscore_server(context, configuration);
  configuration = NULL;
  if (!configured)
    goto cleanup;
  airc_oscore_configure(context, &worker.persistence);
  coap_context_set_max_body_size(context, 16384);
  coap_context_set_session_timeout(context, 30);
  coap_context_set_block_mode(context, COAP_BLOCK_USE_LIBCOAP | COAP_BLOCK_SINGLE_BODY | COAP_BLOCK_NOT_RANDOM_BLOCK1);
  if (!coap_context_set_max_block_size(context, 1024))
    goto cleanup;
  resource = coap_resource_init(coap_make_str_const("v1/coap/rpc"), COAP_RESOURCE_FLAGS_OSCORE_ONLY);
  if (resource == NULL)
    goto cleanup;
  coap_resource_set_userdata(resource, &worker);
  coap_register_handler(resource, COAP_REQUEST_POST, airc_oscore_rpc);
  coap_add_resource(context, resource);
  endpoint = coap_new_endpoint(context, &address, COAP_PROTO_UDP);
  if (endpoint == NULL)
    goto cleanup;
  coap_endpoint_set_default_mtu(endpoint, 1200);
  port = coap_address_get_port(&endpoint->bind_addr);
  ready[4] = (uint8_t)(port>>8);
  ready[5] = (uint8_t)port;
  now = airc_oscore_now();
  if (now < 0 || !airc_oscore_io(4, ready, sizeof(ready), 1, now+3000))
    goto cleanup;
  while (!stopped && !worker.persistence.failed && !worker.rpc_failed && !owner_gone()) {
    if (!airc_oscore_expire(context) || coap_io_process(context, 100) < 0)
      goto cleanup;
  }
  if (!worker.persistence.failed && !worker.rpc_failed)
    result = 0;
cleanup:
  coap_free_context(context);
  if (configuration != NULL)
    coap_delete_oscore_conf(configuration);
  close(3);
  close(4);
  coap_cleanup();
  if (result != 0)
    fputs("native OSCORE worker failed\n", stderr);
  return result;
}
