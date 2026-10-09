#include "../mach/client.h"

#include <CoreFoundation/CoreFoundation.h>
#include <dispatch/dispatch.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <xpc/xpc.h>

#define AIRC_UPSTREAM_NAME_CAPACITY 128U

static dispatch_queue_t airc_service_queue;
static char airc_upstream_name[AIRC_UPSTREAM_NAME_CAPACITY];
static atomic_bool airc_peer_active = ATOMIC_VAR_INIT(false);

static bool airc_claim_peer(void)
{
    bool expected = false;
    return atomic_compare_exchange_strong_explicit(&airc_peer_active, &expected, true,
                                                  memory_order_acq_rel,
                                                  memory_order_acquire);
}

static void airc_release_peer(void)
{
    atomic_store_explicit(&airc_peer_active, false, memory_order_release);
}

static void airc_send_error(xpc_connection_t peer, xpc_object_t request,
                            const char *description)
{
    xpc_object_t reply = xpc_dictionary_create_reply(request);

    if (reply == NULL) {
        xpc_connection_cancel(peer);
        return;
    }
    xpc_dictionary_set_string(reply, "error", description);
    xpc_connection_send_message(peer, reply);
    xpc_release(reply);
}

static bool airc_extract_single_frame(xpc_object_t message,
                                      const uint8_t **frame,
                                      size_t *frame_length)
{
    xpc_object_t value;

    if (xpc_dictionary_get_count(message) != 1U) {
        return false;
    }
    value = xpc_dictionary_get_value(message, "frame");
    if (value == NULL || xpc_get_type(value) != XPC_TYPE_DATA) {
        return false;
    }
    *frame = (const uint8_t *)xpc_data_get_bytes_ptr(value);
    *frame_length = xpc_data_get_length(value);
    return *frame != NULL &&
           *frame_length != 0U && *frame_length <= AIRC_MACH_REQUEST_MAX;
}

static void airc_handle_message(xpc_connection_t peer, xpc_object_t message)
{
    const uint8_t *frame = NULL;
    size_t frame_length = 0U;
    uint8_t response[AIRC_MACH_RESPONSE_MAX];
    size_t response_length = 0U;
    airc_mach_status_t status;
    xpc_object_t reply;

    if (xpc_connection_get_euid(peer) != geteuid()) {
        airc_send_error(peer, message, "peer identity refused");
        return;
    }
    if (!airc_extract_single_frame(message, &frame, &frame_length)) {
        airc_send_error(peer, message, "invalid frame request");
        return;
    }

    status = airc_mach_transact(airc_upstream_name, frame, frame_length,
                                response, sizeof(response), &response_length);
    if (status != AIRC_MACH_OK || response_length == 0U ||
        response_length > AIRC_MACH_RESPONSE_MAX) {
        const char *description = "upstream transaction failed";
        if (status == AIRC_MACH_TIMEOUT) {
            description = "upstream transaction timed out";
        } else if (status == AIRC_MACH_INVALID) {
            description = "upstream response refused";
        }
        airc_send_error(peer, message, description);
        return;
    }

    reply = xpc_dictionary_create_reply(message);
    if (reply == NULL) {
        xpc_connection_cancel(peer);
        return;
    }
    xpc_dictionary_set_data(reply, "response", response, response_length);
    xpc_connection_send_message(peer, reply);
    xpc_release(reply);
}

static void airc_peer_event_handler(xpc_connection_t peer, xpc_object_t event)
{
    if (xpc_get_type(event) == XPC_TYPE_ERROR) {
        if (event == XPC_ERROR_CONNECTION_INVALID) {
            airc_release_peer();
            xpc_release(peer);
        } else {
            xpc_connection_cancel(peer);
        }
        return;
    }
    if (xpc_get_type(event) != XPC_TYPE_DICTIONARY) {
        xpc_connection_cancel(peer);
        return;
    }
    if (xpc_connection_get_context(peer) != NULL) {
        xpc_connection_cancel(peer);
        return;
    }
    xpc_connection_set_context(peer, (void *)(uintptr_t)1U);
    airc_handle_message(peer, event);
}

static void airc_accept_peer(xpc_connection_t peer)
{
    if (xpc_connection_get_euid(peer) != geteuid() || !airc_claim_peer()) {
        xpc_connection_cancel(peer);
        return;
    }
    (void)xpc_retain(peer);
    xpc_connection_set_target_queue(peer, airc_service_queue);
    xpc_connection_set_event_handler(peer, ^(xpc_object_t event) {
        airc_peer_event_handler(peer, event);
    });
    xpc_connection_resume(peer);
}

static int airc_load_upstream_name(char *out_name, size_t out_capacity)
{
    CFBundleRef bundle;
    CFTypeRef value;
    int ok = 0;

    if (out_name == NULL || out_capacity < AIRC_UPSTREAM_NAME_CAPACITY) {
        return 0;
    }
    out_name[0] = '\0';
    bundle = CFBundleGetMainBundle();
    if (bundle == NULL) {
        return 0;
    }
    value = CFBundleGetValueForInfoDictionaryKey(bundle, CFSTR("AIRCUpstreamMachService"));
    if (value == NULL || CFGetTypeID(value) != CFStringGetTypeID()) {
        return 0;
    }
    if (!CFStringGetCString((CFStringRef)value, out_name, (CFIndex)out_capacity,
                            kCFStringEncodingUTF8)) {
        out_name[0] = '\0';
        return 0;
    }
    ok = airc_mach_name_valid(out_name);
    if (!ok) {
        out_name[0] = '\0';
    }
    return ok;
}

int main(void)
{
    if (!airc_load_upstream_name(airc_upstream_name,
                                     sizeof(airc_upstream_name))) {
        fputs("invalid AIRCUpstreamMachService in embedded service Info.plist\n", stderr);
        return EXIT_FAILURE;
    }
    airc_service_queue = dispatch_queue_create("org.airc.xpc.forward.serial",
                                               DISPATCH_QUEUE_SERIAL);
    if (airc_service_queue == NULL) {
        fputs("unable to create XPC service queue\n", stderr);
        return EXIT_FAILURE;
    }
    xpc_main(airc_accept_peer);
    return EXIT_FAILURE;
}
