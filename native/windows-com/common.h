#ifndef AIRC_WINDOWS_COM_COMMON_H
#define AIRC_WINDOWS_COM_COMMON_H

#ifndef COBJMACROS
#define COBJMACROS
#endif
#include "../windows-worker/common.h"

#include <objbase.h>
#include <oaidl.h>
#include <rpc.h>
#include <string.h>

#define AIRC_MAX_OBJREF 4096u
#define AIRC_OBJREF_STD_MIN 68u
#define AIRC_SERVER_RELEASE_ACK 2u

typedef union {
    ACL alignment;
    BYTE bytes[1024];
} airc_acl_storage;

static inline bool airc_validate_objref(const uint8_t *data, uint32_t size) {
    static const uint8_t signature[4] = {'M', 'E', 'O', 'W'};
    static const uint8_t standard_flags[4] = {1, 0, 0, 0};
    if (data == NULL || size < AIRC_OBJREF_STD_MIN || size > AIRC_MAX_OBJREF ||
        memcmp(data, signature, sizeof(signature)) != 0 ||
        memcmp(data + 4, standard_flags, sizeof(standard_flags)) != 0) return false;
    return memcmp(data + 8, &IID_IDispatch, sizeof(IID_IDispatch)) == 0;
}

HRESULT airc_create_dispatch(PSID client_sid, HANDLE finished, IDispatch **dispatch_out);
bool airc_dispatch_stop_and_wait(IDispatch *dispatch, DWORD timeout_ms);

#endif
