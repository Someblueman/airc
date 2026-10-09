#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dispatch/dispatch.h>
#include <xpc/xpc.h>

struct reply_state {
	dispatch_semaphore_t ready;
	xpc_object_t reply;
};

static xpc_object_t make_request(const char *mode)
{
	xpc_object_t request = xpc_dictionary_create(NULL, NULL, 0);
	if (request == NULL) {
		return NULL;
	}
	if (strcmp(mode, "missing-frame") == 0) {
		return request;
	}
	if (strcmp(mode, "wrong-frame-type") == 0) {
		xpc_dictionary_set_string(request, "frame", "not-data");
		return request;
	}
	if (strcmp(mode, "extra-key") == 0) {
		static const uint8_t frame[] = "{}";
		xpc_dictionary_set_data(request, "frame", frame, sizeof(frame) - 1U);
		xpc_dictionary_set_bool(request, "unexpected", true);
		return request;
	}
	if (strcmp(mode, "oversized-frame") == 0) {
		const size_t length = (size_t)16U * 1024U + 1U;
		uint8_t *frame = calloc(length, sizeof(*frame));
		if (frame == NULL) {
			xpc_release(request);
			return NULL;
		}
		xpc_dictionary_set_data(request, "frame", frame, length);
		free(frame);
		return request;
	}
	xpc_release(request);
	return NULL;
}

int main(int argc, char **argv)
{
	if (argc != 2) {
		fprintf(stderr, "usage: %s missing-frame|wrong-frame-type|extra-key|oversized-frame\n", argv[0]);
		return 2;
	}
	xpc_connection_t connection = xpc_connection_create("org.airc.gateway.xpc.bridge", NULL);
	if (connection == NULL) {
		fprintf(stderr, "could not create XPC connection\n");
		return 1;
	}
	xpc_connection_set_event_handler(connection, ^(xpc_object_t event) { (void)event; });
	xpc_connection_resume(connection);
	xpc_object_t request = make_request(argv[1]);
	if (request == NULL) {
		fprintf(stderr, "could not create malformed request\n");
		xpc_connection_cancel(connection);
		xpc_release(connection);
		return 1;
	}
	struct reply_state *state = calloc(1, sizeof(*state));
	if (state == NULL) {
		fprintf(stderr, "could not allocate XPC reply state\n");
		xpc_release(request);
		xpc_connection_cancel(connection);
		xpc_release(connection);
		return 1;
	}
	state->ready = dispatch_semaphore_create(0);
	if (state->ready == NULL) {
		fprintf(stderr, "could not create XPC reply semaphore\n");
		free(state);
		xpc_release(request);
		xpc_connection_cancel(connection);
		xpc_release(connection);
		return 1;
	}
	xpc_connection_send_message_with_reply(connection, request, dispatch_get_global_queue(QOS_CLASS_DEFAULT, 0), ^(xpc_object_t reply) {
		state->reply = xpc_retain(reply);
		dispatch_semaphore_signal(state->ready);
	});
	if (dispatch_semaphore_wait(state->ready, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC)) != 0) {
		fprintf(stderr, "timed out waiting for XPC refusal\n");
		xpc_release(request);
		xpc_connection_cancel(connection);
		xpc_release(connection);
		return 1;
	}
	xpc_object_t reply = state->reply;
	int result = 1;
	if (reply == NULL || xpc_get_type(reply) != XPC_TYPE_DICTIONARY || xpc_dictionary_get_count(reply) != 1) {
		char *description = reply == NULL ? NULL : xpc_copy_description(reply);
		fprintf(stderr, "malformed request received an invalid XPC response: %s\n",
			description == NULL ? "(null)" : description);
		free(description);
	} else {
		xpc_object_t error = xpc_dictionary_get_value(reply, "error");
		if (error == NULL || xpc_get_type(error) != XPC_TYPE_STRING ||
			xpc_string_get_length(error) == 0 || xpc_string_get_length(error) > 64) {
			fprintf(stderr, "malformed request did not receive one bounded error string\n");
		} else {
			(void)fputs("rejected\n", stdout);
			result = 0;
		}
	}
	if (reply != NULL) {
		xpc_release(reply);
	}
	dispatch_release(state->ready);
	free(state);
	xpc_release(request);
	xpc_connection_cancel(connection);
	xpc_release(connection);
	return result;
}
