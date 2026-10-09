#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <mach/mach.h>
#include <servers/bootstrap.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#define REQUEST_MESSAGE_ID ((int32_t)0x41495201)
#define RESPONSE_MESSAGE_ID ((int32_t)0x41495202)
#define MACH_NOTIFY_SEND_ONCE_ID 71
#define REQUEST_LIMIT (16U * 1024U)
#define RESPONSE_LIMIT (64U * 1024U)

struct inline_message {
    mach_msg_header_t header;
    uint32_t payload_length;
    unsigned char payload[RESPONSE_LIMIT];
};

union received_message {
    struct inline_message message;
    struct {
        struct inline_message message;
        mach_msg_max_trailer_t trailer;
    } with_trailer;
};

_Static_assert(sizeof(mach_msg_header_t) == 24, "Mach header wire size changed");
_Static_assert(offsetof(struct inline_message, payload) == 28,
               "Mach inline payload offset changed");

static size_t aligned_length(size_t length)
{
    return (length + 3U) & ~(size_t)3U;
}

static int read_request(unsigned char *destination, size_t max_length, size_t *length)
{
    size_t used = 0;

    while (used <= max_length) {
        const ssize_t count = read(STDIN_FILENO, destination + used, max_length + 1U - used);
        if (count < 0) {
            if (errno == EINTR) {
                continue;
            }
            (void)fprintf(stderr, "mach client read request: %s\n", strerror(errno));
            return EXIT_FAILURE;
        }
        if (count == 0) {
            *length = used;
            return EXIT_SUCCESS;
        }
        used += (size_t)count;
    }

    (void)fprintf(stderr, "mach client request exceeds %zu bytes\n", max_length);
    return EXIT_FAILURE;
}

static int write_response(const unsigned char *source, size_t length)
{
    size_t used = 0;

    while (used < length) {
        const ssize_t count = write(STDOUT_FILENO, source + used, length - used);
        if (count < 0) {
            if (errno == EINTR) {
                continue;
            }
            (void)fprintf(stderr, "mach client write response: %s\n", strerror(errno));
            return EXIT_FAILURE;
        }
        if (count == 0) {
            (void)fprintf(stderr, "mach client write response: zero-length write\n");
            return EXIT_FAILURE;
        }
        used += (size_t)count;
    }
    return EXIT_SUCCESS;
}

int main(int argc, char **argv)
{
    int result = EXIT_FAILURE;
    int drop_reply = 0;
    int expect_no_reply = 0;
    int allow_oversize = 0;
    mach_port_t service_port = MACH_PORT_NULL;
    mach_port_t reply_port = MACH_PORT_NULL;
    struct inline_message message;
    union received_message received;
    size_t request_length = 0;
    size_t request_limit = REQUEST_LIMIT;
    mach_msg_return_t status;

    if (argc < 2 || argc > 5 || argv[1][0] == '\0') {
        (void)fprintf(stderr, "usage: mach-ipc-client bootstrap-name [--drop-reply] [--expect-no-reply] [--allow-oversize]\n");
        return EXIT_FAILURE;
    }
    for (int i = 2; i < argc; ++i) {
        if (strcmp(argv[i], "--drop-reply") == 0 && !drop_reply && !expect_no_reply) {
            drop_reply = 1;
        } else if (strcmp(argv[i], "--expect-no-reply") == 0 && !drop_reply && !expect_no_reply) {
            expect_no_reply = 1;
        } else if (strcmp(argv[i], "--allow-oversize") == 0 && !allow_oversize) {
            allow_oversize = 1;
            request_limit++;
        } else {
            (void)fprintf(stderr, "mach client has invalid or repeated options\n");
            return EXIT_FAILURE;
        }
    }
    memset(&message, 0, sizeof(message));
    memset(&received, 0, sizeof(received));
    if (read_request(message.payload, request_limit, &request_length) != EXIT_SUCCESS ||
        request_length == 0U) {
        if (request_length == 0U) {
            (void)fprintf(stderr, "mach client request is empty\n");
        }
        goto cleanup;
    }
    message.payload_length = (uint32_t)request_length;

    status = bootstrap_look_up(bootstrap_port, argv[1], &service_port);
    if (status != BOOTSTRAP_SUCCESS) {
        (void)fprintf(stderr, "mach client bootstrap_look_up failed: %d\n", status);
        goto cleanup;
    }
    status = mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &reply_port);
    if (status != KERN_SUCCESS) {
        (void)fprintf(stderr, "mach client mach_port_allocate failed: %d\n", status);
        goto cleanup;
    }

    message.header.msgh_bits = MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND,
                                               MACH_MSG_TYPE_MAKE_SEND_ONCE);
    message.header.msgh_size = (mach_msg_size_t)(sizeof(message.header) + sizeof(message.payload_length) +
                                                 aligned_length(request_length));
    message.header.msgh_remote_port = service_port;
    message.header.msgh_local_port = reply_port;
    message.header.msgh_id = REQUEST_MESSAGE_ID;
    status = mach_msg(&message.header, MACH_SEND_MSG | MACH_SEND_TIMEOUT, message.header.msgh_size,
                      0, MACH_PORT_NULL, 5000, MACH_PORT_NULL);
    if (status != MACH_MSG_SUCCESS) {
        (void)fprintf(stderr, "mach client request send failed: %d\n", status);
        goto cleanup;
    }
    if (drop_reply) {
        result = EXIT_SUCCESS;
        goto cleanup;
    }

    status = mach_msg(&received.message.header,
                      MACH_RCV_MSG | MACH_RCV_TIMEOUT |
                          MACH_RCV_TRAILER_TYPE(MACH_MSG_TRAILER_FORMAT_0) |
                          MACH_RCV_TRAILER_ELEMENTS(MACH_RCV_TRAILER_NULL),
                      0, (mach_msg_size_t)sizeof(received), reply_port,
                      expect_no_reply ? 1200 : 8000, MACH_PORT_NULL);
    if (expect_no_reply && (status == MACH_RCV_TIMED_OUT || status == MACH_RCV_PORT_DIED)) {
        result = EXIT_SUCCESS;
        goto cleanup;
    }
    if (status != MACH_MSG_SUCCESS) {
        (void)fprintf(stderr, "mach client response receive failed: %d\n", status);
        goto cleanup;
    }
    if (expect_no_reply && received.message.header.msgh_id == MACH_NOTIFY_SEND_ONCE_ID &&
        received.message.header.msgh_size == sizeof(mach_msg_header_t) &&
        received.message.header.msgh_bits == MACH_MSGH_BITS(0, MACH_MSG_TYPE_MOVE_SEND_ONCE) &&
        received.message.header.msgh_remote_port == MACH_PORT_NULL &&
        received.message.header.msgh_local_port == reply_port &&
        received.message.header.msgh_voucher_port == MACH_PORT_NULL) {
        result = EXIT_SUCCESS;
        goto cleanup;
    }
    if ((received.message.header.msgh_bits & MACH_MSGH_BITS_COMPLEX) != 0U ||
        received.message.header.msgh_remote_port != MACH_PORT_NULL ||
        received.message.header.msgh_local_port != reply_port ||
        received.message.header.msgh_voucher_port != MACH_PORT_NULL) {
        (void)fprintf(stderr, "mach client response carried unexpected ports or descriptors: id=%d size=%u bits=%#x remote=%u local=%u voucher=%u expected-local=%u notify-bits=%#x\n",
                      received.message.header.msgh_id, received.message.header.msgh_size,
                      received.message.header.msgh_bits, received.message.header.msgh_remote_port,
                      received.message.header.msgh_local_port, received.message.header.msgh_voucher_port, reply_port,
                      MACH_MSGH_BITS(0, MACH_MSG_TYPE_MOVE_SEND_ONCE));
        mach_msg_destroy(&received.message.header);
        goto cleanup;
    }
    if (received.message.header.msgh_id != RESPONSE_MESSAGE_ID ||
        received.message.header.msgh_size < sizeof(received.message.header) + sizeof(received.message.payload_length) ||
        received.message.payload_length == 0U || received.message.payload_length > RESPONSE_LIMIT ||
        received.message.header.msgh_size != sizeof(received.message.header) + sizeof(received.message.payload_length) +
                                                 aligned_length(received.message.payload_length)) {
        (void)fprintf(stderr, "mach client response framing is invalid: id=%d size=%u length=%u expected-id=%d aligned=%zu\n",
                      received.message.header.msgh_id, received.message.header.msgh_size,
                      received.message.payload_length, RESPONSE_MESSAGE_ID,
                      sizeof(received.message.header) + sizeof(received.message.payload_length) +
                          aligned_length(received.message.payload_length));
        mach_msg_destroy(&received.message.header);
        goto cleanup;
    }
    for (size_t i = received.message.payload_length; i < aligned_length(received.message.payload_length); ++i) {
        if (received.message.payload[i] != 0U) {
            (void)fprintf(stderr, "mach client response padding is nonzero\n");
            mach_msg_destroy(&received.message.header);
            goto cleanup;
        }
    }
    if (write_response(received.message.payload, received.message.payload_length) != EXIT_SUCCESS) {
        goto cleanup;
    }
    result = EXIT_SUCCESS;

cleanup:
    if (reply_port != MACH_PORT_NULL) {
        const mach_msg_return_t release_status = mach_port_mod_refs(
            mach_task_self(), reply_port, MACH_PORT_RIGHT_RECEIVE, -1);
        if (release_status != KERN_SUCCESS && result == EXIT_SUCCESS) {
            (void)fprintf(stderr, "mach client release reply port failed: %d\n", release_status);
            result = EXIT_FAILURE;
        }
    }
    if (service_port != MACH_PORT_NULL) {
        const mach_msg_return_t release_status = mach_port_deallocate(mach_task_self(), service_port);
        if (release_status != KERN_SUCCESS && result == EXIT_SUCCESS) {
            (void)fprintf(stderr, "mach client release service port failed: %d\n", release_status);
            result = EXIT_FAILURE;
        }
    }
    return result;
}
