#ifndef AIRC_XPC_MACH_H
#define AIRC_XPC_MACH_H

#include <stddef.h>
#include <stdint.h>

enum {
    AIRC_MACH_REQUEST_MAX = 16 * 1024,
    AIRC_MACH_RESPONSE_MAX = 64 * 1024,
    AIRC_MACH_REQUEST_ID = 0x41495201,
    AIRC_MACH_RESPONSE_ID = 0x41495202
};

typedef enum airc_mach_status {
    AIRC_MACH_OK = 0,
    AIRC_MACH_INVALID = 1,
    AIRC_MACH_UNAVAILABLE = 2,
    AIRC_MACH_TIMEOUT = 3
} airc_mach_status_t;

/* Read AIRCUpstreamMachService from this embedded service's own Info.plist.
 * The caller owns out_name and supplies at least 128 bytes. */
int airc_mach_load_upstream_name(char *out_name, size_t out_capacity);

/* Forward one opaque frame through the existing simple Mach IPC wire contract.
 * Buffers are caller-owned; no frame or response is retained after return. */
airc_mach_status_t airc_mach_transact(const char *service_name,
                                      const uint8_t *frame,
                                      size_t frame_length,
                                      uint8_t *response,
                                      size_t response_capacity,
                                      size_t *response_length);

#endif
