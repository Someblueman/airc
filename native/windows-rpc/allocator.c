#include "../windows-worker/common.h"

#include <rpc.h>
#include <stddef.h>
#include <stdlib.h>

#define AIRC_MAX_RPC_ALLOC 524288u

typedef union {
    struct {
        size_t bytes;
    } value;
    max_align_t alignment;
} allocation_header;

static volatile LONG64 g_rpc_allocated = 0;

void *__RPC_USER MIDL_user_allocate(size_t size) {
    size_t payload = size == 0 ? 1 : size;
    size_t total;
    LONG64 observed;
    allocation_header *header;
    if (payload > (128u * 1024u) || payload > SIZE_MAX - sizeof(*header)) return NULL;
    total = sizeof(*header) + payload;
    if (total > (128u * 1024u) || total > AIRC_MAX_RPC_ALLOC) return NULL;
    for (;;) {
        observed = InterlockedCompareExchange64(&g_rpc_allocated, 0, 0);
        if (observed < 0 || (uint64_t)observed > AIRC_MAX_RPC_ALLOC - total) return NULL;
        if (InterlockedCompareExchange64(&g_rpc_allocated, observed + (LONG64)total, observed) == observed) break;
    }
    header = (allocation_header *)malloc(total);
    if (header == NULL) {
        (void)InterlockedAdd64(&g_rpc_allocated, -(LONG64)total);
        return NULL;
    }
    header->value.bytes = total;
    return (void *)(header + 1);
}

void __RPC_USER MIDL_user_free(void *pointer) {
    allocation_header *header;
    if (pointer == NULL) return;
    header = ((allocation_header *)pointer) - 1;
    (void)InterlockedAdd64(&g_rpc_allocated, -(LONG64)header->value.bytes);
    free(header);
}
