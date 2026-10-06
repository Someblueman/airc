/* SPDX-License-Identifier: BSD-2-Clause */
#ifndef AIRC_OSCORE_IPC_H
#define AIRC_OSCORE_IPC_H
#include <stddef.h>
#include <stdint.h>
/* Caller owns a single connected private socket. I/O uses a monotonic absolute
 * deadline; prepare once before use. No helper closes the socket. */
int airc_oscore_socket(int fd);
int64_t airc_oscore_now(void);
int airc_oscore_io(int fd, uint8_t *bytes, size_t length, int write_data, int64_t deadline);
#endif
