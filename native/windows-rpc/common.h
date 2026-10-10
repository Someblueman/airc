#ifndef AIRC_WINDOWS_RPC_COMMON_H
#define AIRC_WINDOWS_RPC_COMMON_H

#include <windows.h>
#include <rpc.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#define AIRC_MAX_REQUEST 16384u
#define AIRC_MAX_RESPONSE 65536u
#define AIRC_MAX_RPC_ALLOC 524288u

bool airc_parse_sid(const char *value, PSID *sid_out);
bool airc_token_matches(PSID expected);
bool airc_stdio_is_piped(void);
bool airc_read_frame(HANDLE input, uint32_t maximum, uint8_t *buffer, uint32_t *length);
bool airc_write_frame(HANDLE output, const uint8_t *buffer, uint32_t length);
void airc_diagnostic(const char *message);
bool airc_apply_job_limits(void);
bool airc_start_watchdog(void);
void airc_stop_watchdog(void);

void *__RPC_USER MIDL_user_allocate(size_t size);
void __RPC_USER MIDL_user_free(void *pointer);

#endif
