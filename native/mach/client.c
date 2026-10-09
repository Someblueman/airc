#include "client.h"

#include <mach/mach.h>
#include <mach/message.h>
#include <servers/bootstrap.h>
#include <time.h>
#include <string.h>

#define AIRC_MACH_NAME_MAX 127
#define AIRC_TRANSACTION_TIMEOUT_MS 4000
#define AIRC_BODY_PREFIX_SIZE 4

_Static_assert(sizeof(mach_msg_header_t) == 24, "Mach header ABI changed");

struct airc_request_message {
    mach_msg_header_t header;
    uint8_t body[AIRC_BODY_PREFIX_SIZE + AIRC_MACH_REQUEST_MAX + 3];
};

struct airc_response_message {
    mach_msg_header_t header;
    uint8_t body[AIRC_BODY_PREFIX_SIZE + AIRC_MACH_RESPONSE_MAX + 3];
    mach_msg_max_trailer_t trailer;
};

int airc_mach_name_valid(const char *name)
{
    size_t length;
    size_t i;

    if (name == NULL) {
        return 0;
    }
    length = strnlen(name, AIRC_MACH_NAME_MAX + 1U);
    if (length == 0U || length > AIRC_MACH_NAME_MAX) {
        return 0;
    }
    for (i = 0U; i < length; ++i) {
        const unsigned char c = (unsigned char)name[i];
        const int alphanumeric = (c >= (unsigned char)'a' && c <= (unsigned char)'z') ||
                                 (c >= (unsigned char)'A' && c <= (unsigned char)'Z') ||
                                 (c >= (unsigned char)'0' && c <= (unsigned char)'9');
        if (!alphanumeric && (i == 0U || (c != (unsigned char)'.' &&
                                         c != (unsigned char)'_' &&
                                         c != (unsigned char)'-'))) {
            return 0;
        }
    }
    return 1;
}

static int airc_remaining_timeout_ms(const struct timespec *deadline,
                                     mach_msg_timeout_t *timeout_ms)
{
    struct timespec now;
    int64_t seconds;
    int64_t nanoseconds;
    uint64_t milliseconds;

    if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) {
        return 0;
    }
    seconds = (int64_t)deadline->tv_sec - (int64_t)now.tv_sec;
    nanoseconds = (int64_t)deadline->tv_nsec - (int64_t)now.tv_nsec;
    if (nanoseconds < 0) {
        --seconds;
        nanoseconds += INT64_C(1000000000);
    }
    if (seconds < 0 || (seconds == 0 && nanoseconds == 0)) {
        return 0;
    }
    milliseconds = (uint64_t)seconds * UINT64_C(1000) +
                   ((uint64_t)nanoseconds + UINT64_C(999999)) / UINT64_C(1000000);
    if (milliseconds == 0U) {
        milliseconds = 1U;
    }
    if (milliseconds > (uint64_t)UINT32_MAX) {
        milliseconds = (uint64_t)UINT32_MAX;
    }
    *timeout_ms = (mach_msg_timeout_t)milliseconds;
    return 1;
}

static void airc_put_le32(uint8_t out[4], uint32_t value)
{
    out[0] = (uint8_t)(value & UINT32_C(0xff));
    out[1] = (uint8_t)((value >> 8U) & UINT32_C(0xff));
    out[2] = (uint8_t)((value >> 16U) & UINT32_C(0xff));
    out[3] = (uint8_t)((value >> 24U) & UINT32_C(0xff));
}

static uint32_t airc_get_le32(const uint8_t in[4])
{
    return (uint32_t)in[0] | ((uint32_t)in[1] << 8U) |
           ((uint32_t)in[2] << 16U) | ((uint32_t)in[3] << 24U);
}

static size_t airc_aligned4(size_t length)
{
    return (length + 3U) & ~(size_t)3U;
}

static void airc_destroy_received(mach_msg_header_t *header)
{
    if (header != NULL) {
        mach_msg_destroy(header);
    }
}

static int airc_destroy_failed_send(mach_msg_header_t *header)
{
    mach_msg_type_name_t remote_disposition;
    mach_msg_type_name_t local_disposition;
    int pseudo_received;
    int cleanup_ok = 1;

    if (header == NULL) {
        return 1;
    }
    remote_disposition = MACH_MSGH_BITS_REMOTE(header->msgh_bits);
    local_disposition = MACH_MSGH_BITS_LOCAL(header->msgh_bits);
    pseudo_received = remote_disposition == MACH_MSG_TYPE_MOVE_SEND ||
                      remote_disposition == MACH_MSG_TYPE_MOVE_SEND_ONCE ||
                      local_disposition == MACH_MSG_TYPE_MOVE_SEND ||
                      local_disposition == MACH_MSG_TYPE_MOVE_SEND_ONCE;
    if (!pseudo_received) {
        /* A copyin failure leaves the caller's original header untouched. */
        return 1;
    }

    /* mach_msg_destroy deliberately skips msgh_local_port. Pseudo-receive
     * restores a moved local send/send-once right there, so dispose it first. */
    if (header->msgh_local_port != MACH_PORT_NULL &&
        (local_disposition == MACH_MSG_TYPE_MOVE_SEND ||
         local_disposition == MACH_MSG_TYPE_MOVE_SEND_ONCE)) {
        if (mach_port_deallocate(mach_task_self(), header->msgh_local_port) != KERN_SUCCESS) {
            cleanup_ok = 0;
        }
        header->msgh_local_port = MACH_PORT_NULL;
    }
    mach_msg_destroy(header);
    return cleanup_ok;
}

static int airc_mach_result_matches(mach_msg_return_t result,
                                    mach_msg_return_t expected)
{
    return (result & ~MACH_MSG_MASK) == expected;
}

static int airc_release_send_right(mach_port_t *port)
{
    kern_return_t result;

    if (port == NULL || *port == MACH_PORT_NULL) {
        return 1;
    }
    result = mach_port_deallocate(mach_task_self(), *port);
    *port = MACH_PORT_NULL;
    return result == KERN_SUCCESS;
}

static int airc_release_receive_right(mach_port_t *port)
{
    kern_return_t result;

    if (port == NULL || *port == MACH_PORT_NULL) {
        return 1;
    }
    result = mach_port_mod_refs(mach_task_self(), *port, MACH_PORT_RIGHT_RECEIVE, -1);
    *port = MACH_PORT_NULL;
    return result == KERN_SUCCESS;
}

airc_mach_status_t airc_mach_transact(const char *service_name,
                                      const uint8_t *frame,
                                      size_t frame_length,
                                      uint8_t *response,
                                      size_t response_capacity,
                                      size_t *response_length)
{
    struct airc_request_message request;
    struct airc_response_message received;
    struct timespec deadline;
    mach_port_t service = MACH_PORT_NULL;
    mach_port_t reply = MACH_PORT_NULL;
    mach_msg_timeout_t timeout_ms = 0U;
    mach_msg_return_t result;
    airc_mach_status_t status = AIRC_MACH_UNAVAILABLE;
    size_t request_size;
    size_t response_body_size;
    size_t aligned_response_body_size;
    uint32_t response_length_field;
    size_t i;

    memset(&request, 0, sizeof(request));
    memset(&received, 0, sizeof(received));
    if (response_length != NULL) {
        *response_length = 0U;
    }
    if (!airc_mach_name_valid(service_name) || frame == NULL || frame_length == 0U ||
        frame_length > AIRC_MACH_REQUEST_MAX || response == NULL ||
        response_capacity < AIRC_MACH_RESPONSE_MAX || response_length == NULL) {
        return AIRC_MACH_INVALID;
    }
    if (clock_gettime(CLOCK_MONOTONIC, &deadline) != 0) {
        return AIRC_MACH_UNAVAILABLE;
    }
    deadline.tv_sec += (time_t)(AIRC_TRANSACTION_TIMEOUT_MS / 1000);

    /* bootstrap_look_up is a synchronous SDK call without cancellation; the
     * common deadline still bounds every Mach send and receive that follows. */
    result = bootstrap_look_up(bootstrap_port, service_name, &service);
    if (result != KERN_SUCCESS) {
        goto cleanup;
    }
    if (!airc_remaining_timeout_ms(&deadline, &timeout_ms)) {
        status = AIRC_MACH_TIMEOUT;
        goto cleanup;
    }
    result = mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &reply);
    if (result != KERN_SUCCESS) {
        goto cleanup;
    }

    request_size = sizeof(mach_msg_header_t) + AIRC_BODY_PREFIX_SIZE + frame_length;
    request_size = airc_aligned4(request_size);
    request.header.msgh_bits = MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND,
                                              MACH_MSG_TYPE_MAKE_SEND_ONCE);
    request.header.msgh_size = (mach_msg_size_t)request_size;
    request.header.msgh_remote_port = service;
    request.header.msgh_local_port = reply;
    request.header.msgh_voucher_port = MACH_PORT_NULL;
    request.header.msgh_id = AIRC_MACH_REQUEST_ID;
    airc_put_le32(request.body, (uint32_t)frame_length);
    memcpy(request.body + AIRC_BODY_PREFIX_SIZE, frame, frame_length);

    if (!airc_remaining_timeout_ms(&deadline, &timeout_ms)) {
        status = AIRC_MACH_TIMEOUT;
        goto cleanup;
    }
    result = mach_msg(&request.header, MACH_SEND_MSG | MACH_SEND_TIMEOUT,
                      request.header.msgh_size, 0U, MACH_PORT_NULL, timeout_ms,
                      MACH_PORT_NULL);
    if (result != MACH_MSG_SUCCESS) {
        status = airc_mach_result_matches(result, MACH_SEND_TIMED_OUT)
                     ? AIRC_MACH_TIMEOUT
                     : AIRC_MACH_UNAVAILABLE;
        /* Pseudo-receive returns moved rights in the send buffer on failure. */
        if (!airc_destroy_failed_send(&request.header) && status == AIRC_MACH_TIMEOUT) {
            status = AIRC_MACH_UNAVAILABLE;
        }
        goto cleanup;
    }
    if (!airc_release_send_right(&service)) {
        status = AIRC_MACH_UNAVAILABLE;
        goto cleanup;
    }

    if (!airc_remaining_timeout_ms(&deadline, &timeout_ms)) {
        status = AIRC_MACH_TIMEOUT;
        goto cleanup;
    }
    result = mach_msg(&received.header,
                      MACH_RCV_MSG | MACH_RCV_TIMEOUT |
                          MACH_RCV_TRAILER_TYPE(MACH_MSG_TRAILER_FORMAT_0) |
                          MACH_RCV_TRAILER_ELEMENTS(MACH_RCV_TRAILER_AUDIT),
                      0U, (mach_msg_size_t)sizeof(received), reply, timeout_ms,
                      MACH_PORT_NULL);
    if (result != MACH_MSG_SUCCESS) {
        status = airc_mach_result_matches(result, MACH_RCV_TIMED_OUT)
                     ? AIRC_MACH_TIMEOUT
                     : AIRC_MACH_UNAVAILABLE;
        if (airc_mach_result_matches(result, MACH_RCV_BODY_ERROR)) {
            airc_destroy_received(&received.header);
            status = AIRC_MACH_INVALID;
        } else if (airc_mach_result_matches(result, MACH_RCV_HEADER_ERROR) ||
                   airc_mach_result_matches(result, MACH_RCV_TOO_LARGE)) {
            status = AIRC_MACH_INVALID;
        }
        goto cleanup;
    }

    if (received.header.msgh_id != AIRC_MACH_RESPONSE_ID ||
        received.header.msgh_remote_port != MACH_PORT_NULL ||
        received.header.msgh_local_port != reply ||
        received.header.msgh_voucher_port != MACH_PORT_NULL ||
        received.header.msgh_bits != MACH_MSGH_BITS(0, MACH_MSG_TYPE_MOVE_SEND_ONCE) ||
        received.header.msgh_size < sizeof(mach_msg_header_t) + AIRC_BODY_PREFIX_SIZE ||
        received.header.msgh_size > sizeof(mach_msg_header_t) + AIRC_BODY_PREFIX_SIZE +
                                        AIRC_MACH_RESPONSE_MAX + 3U) {
        status = AIRC_MACH_INVALID;
        airc_destroy_received(&received.header);
        goto cleanup;
    }
    response_body_size = (size_t)received.header.msgh_size - sizeof(mach_msg_header_t);
    response_length_field = airc_get_le32(received.body);
    if (response_length_field == 0U || response_length_field > AIRC_MACH_RESPONSE_MAX ||
        response_capacity < (size_t)response_length_field) {
        status = AIRC_MACH_INVALID;
        airc_destroy_received(&received.header);
        goto cleanup;
    }
    aligned_response_body_size = AIRC_BODY_PREFIX_SIZE +
                                 airc_aligned4((size_t)response_length_field);
    if (response_body_size != aligned_response_body_size) {
        status = AIRC_MACH_INVALID;
        airc_destroy_received(&received.header);
        goto cleanup;
    }
    for (i = AIRC_BODY_PREFIX_SIZE + (size_t)response_length_field;
         i < response_body_size; ++i) {
        if (received.body[i] != 0U) {
            status = AIRC_MACH_INVALID;
            airc_destroy_received(&received.header);
            goto cleanup;
        }
    }
    memcpy(response, received.body + AIRC_BODY_PREFIX_SIZE, (size_t)response_length_field);
    *response_length = (size_t)response_length_field;
    status = AIRC_MACH_OK;

cleanup:
    if (!airc_release_send_right(&service) && status == AIRC_MACH_OK) {
        status = AIRC_MACH_UNAVAILABLE;
        *response_length = 0U;
    }
    if (!airc_release_receive_right(&reply) && status == AIRC_MACH_OK) {
        status = AIRC_MACH_UNAVAILABLE;
        *response_length = 0U;
    }
    return status;
}
