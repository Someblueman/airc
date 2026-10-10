#include <dispatch/dispatch.h>
#include <errno.h>
#include <poll.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
#include <xpc/xpc.h>

enum {
	FRAME_LIMIT = 16 * 1024,
	RESPONSE_LIMIT = 64 * 1024,
	ERROR_LIMIT = 1024,
	WAIT_SECONDS = 8
};

struct reply_state {
	dispatch_semaphore_t ready;
	xpc_object_t reply;
};

static int remaining_milliseconds(const struct timespec *deadline);

static void terminate_on_signal(int signal_number) {
	if (signal_number == SIGALRM) {
		_exit(124);
	}
	_exit(128 + signal_number);
}

static int install_signal_handlers(void) {
	struct sigaction action;
	memset(&action, 0, sizeof(action));
	action.sa_handler = terminate_on_signal;
	if (sigemptyset(&action.sa_mask) != 0) {
		return -1;
	}
	if (sigaction(SIGALRM, &action, NULL) != 0) {
		return -1;
	}
	struct sigaction ignore_pipe;
	memset(&ignore_pipe, 0, sizeof(ignore_pipe));
	ignore_pipe.sa_handler = SIG_IGN;
	if (sigemptyset(&ignore_pipe.sa_mask) != 0 ||
	    sigaction(SIGPIPE, &ignore_pipe, NULL) != 0) {
		return -1;
	}
	(void)alarm(WAIT_SECONDS);
	return 0;
}

static int write_all(int fd, const uint8_t *data, size_t length,
		const struct timespec *deadline) {
	size_t written = 0;
	int result = 0;
	while (written < length) {
		int timeout = remaining_milliseconds(deadline);
		if (timeout <= 0) {
			errno = ETIMEDOUT;
			result = -1;
			break;
		}
		struct pollfd output = {.fd = fd, .events = POLLOUT};
		int ready = poll(&output, 1, timeout);
		if (ready < 0 && errno == EINTR) {
			continue;
		}
		if (ready < 0) {
			result = -1;
			break;
		}
		if (ready == 0) {
			errno = ETIMEDOUT;
			result = -1;
			break;
		}
		if ((output.revents & (POLLERR | POLLNVAL)) != 0) {
			errno = EIO;
			result = -1;
			break;
		}
		ssize_t count = write(fd, data + written, length - written);
		if (count < 0 && errno == EINTR) {
			continue;
		}
		if (count <= 0) {
			result = -1;
			break;
		}
		written += (size_t)count;
	}
	return result;
}

static int remaining_milliseconds(const struct timespec *deadline) {
	struct timespec now;
	if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) {
		return -1;
	}
	int64_t seconds = (int64_t)deadline->tv_sec - (int64_t)now.tv_sec;
	int64_t nanoseconds = (int64_t)deadline->tv_nsec - (int64_t)now.tv_nsec;
	int64_t total = seconds * INT64_C(1000000000) + nanoseconds;
	if (total <= 0) {
		return 0;
	}
	int64_t milliseconds = (total + INT64_C(999999)) / INT64_C(1000000);
	return milliseconds > INT32_MAX ? INT32_MAX : (int)milliseconds;
}

static int read_frame(uint8_t frame[FRAME_LIMIT + 1], size_t *length,
		const struct timespec *deadline) {
	int result = -1;
	*length = 0;
	for (;;) {
		int timeout = remaining_milliseconds(deadline);
		if (timeout <= 0) {
			fprintf(stderr, "timed out reading request from stdin\n");
			break;
		}
		struct pollfd input = {.fd = STDIN_FILENO, .events = POLLIN};
		int ready = poll(&input, 1, timeout);
		if (ready < 0 && errno == EINTR) {
			continue;
		}
		if (ready < 0) {
			perror("poll stdin");
			break;
		}
		if (ready == 0) {
			fprintf(stderr, "timed out reading request from stdin\n");
			break;
		}
		if ((input.revents & (POLLERR | POLLNVAL)) != 0) {
			fprintf(stderr, "stdin is not readable\n");
			break;
		}
		if (*length == FRAME_LIMIT + 1) {
			fprintf(stderr, "request exceeds %d bytes\n", FRAME_LIMIT);
			break;
		}
		size_t available = FRAME_LIMIT + 1 - *length;
		ssize_t count = read(STDIN_FILENO, frame + *length, available);
		if (count < 0 && errno == EINTR) {
			continue;
		}
		if (count < 0) {
			perror("read stdin");
			break;
		}
		if (count == 0) {
			if (*length == 0) {
				fprintf(stderr, "request stdin is empty\n");
				break;
			}
			result = 0;
			break;
		}
		*length += (size_t)count;
		if (*length > FRAME_LIMIT) {
			fprintf(stderr, "request exceeds %d bytes\n", FRAME_LIMIT);
			break;
		}
	}
	return result;
}

static void release_reply_state(struct reply_state *state) {
	if (state == NULL) {
		return;
	}
	if (state->reply != NULL) {
		xpc_release(state->reply);
	}
	dispatch_release(state->ready);
	free(state);
}

static int valid_success_reply(xpc_object_t reply, const uint8_t **data, size_t *length) {
	if (xpc_get_type(reply) != XPC_TYPE_DICTIONARY || xpc_dictionary_get_count(reply) != 1) {
		return -1;
	}
	xpc_object_t value = xpc_dictionary_get_value(reply, "response");
	if (value == NULL || xpc_get_type(value) != XPC_TYPE_DATA) {
		return -1;
	}
	*length = xpc_data_get_length(value);
	if (*length == 0 || *length > RESPONSE_LIMIT) {
		return -1;
	}
	*data = xpc_data_get_bytes_ptr(value);
	return *data == NULL ? -1 : 0;
}

static int print_error_reply(xpc_object_t reply) {
	if (xpc_get_type(reply) != XPC_TYPE_DICTIONARY || xpc_dictionary_get_count(reply) != 1) {
		return -1;
	}
	xpc_object_t value = xpc_dictionary_get_value(reply, "error");
	if (value == NULL || xpc_get_type(value) != XPC_TYPE_STRING) {
		return -1;
	}
	const char *message = xpc_string_get_string_ptr(value);
	size_t length = xpc_string_get_length(value);
	if (message == NULL || length == 0 || length > ERROR_LIMIT || strnlen(message, length + 1) != length) {
		return -1;
	}
	(void)fputs("gateway error: ", stderr);
	(void)fwrite(message, 1, length, stderr);
	(void)fputc('\n', stderr);
	return 0;
}

int main(int argc, char **argv) {
	int drop_reply = 0;
	if (argc == 2 && strcmp(argv[1], "--drop-reply") == 0) {
		drop_reply = 1;
	} else if (argc != 1) {
		fprintf(stderr, "usage: %s [--drop-reply]\n", argv[0]);
		return 2;
	}
	if (install_signal_handlers() != 0) {
		perror("install signal handlers");
		return 1;
	}

	struct timespec deadline;
	if (clock_gettime(CLOCK_MONOTONIC, &deadline) != 0) {
		perror("clock_gettime");
		return 1;
	}
	deadline.tv_sec += WAIT_SECONDS;
	uint8_t frame[FRAME_LIMIT + 1];
	size_t frame_length = 0;
	if (read_frame(frame, &frame_length, &deadline) != 0) {
		return 1;
	}

	int result = 1;
	xpc_connection_t connection = NULL;
	xpc_object_t request = NULL;
	struct reply_state *state = NULL;
	int callback_finished = 0;
	connection = xpc_connection_create("org.airc.gateway.xpc.bridge", NULL);
	if (connection == NULL) {
		fprintf(stderr, "could not create XPC connection\n");
		goto cleanup;
	}
	xpc_connection_set_event_handler(connection, ^(xpc_object_t event) {
		(void)event;
	});
	xpc_connection_resume(connection);
	request = xpc_dictionary_create(NULL, NULL, 0);
	if (request == NULL) {
		fprintf(stderr, "could not create XPC request\n");
		goto cleanup;
	}
	xpc_dictionary_set_data(request, "frame", frame, frame_length);
	state = calloc(1, sizeof(*state));
	if (state == NULL) {
		perror("calloc reply state");
		goto cleanup;
	}
	state->ready = dispatch_semaphore_create(0);
	if (state->ready == NULL) {
		fprintf(stderr, "could not create XPC reply wait\n");
		goto cleanup;
	}
	xpc_connection_send_message_with_reply(connection, request, dispatch_get_global_queue(QOS_CLASS_DEFAULT, 0), ^(xpc_object_t reply) {
		state->reply = xpc_retain(reply);
		dispatch_semaphore_signal(state->ready);
	});
	int timeout = remaining_milliseconds(&deadline);
	if (timeout <= 0 || dispatch_semaphore_wait(state->ready, dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeout * INT64_C(1000000))) != 0) {
		fprintf(stderr, "timed out waiting for XPC reply\n");
		goto cleanup;
	}
	callback_finished = 1;
	const uint8_t *response = NULL;
	size_t response_length = 0;
	if (valid_success_reply(state->reply, &response, &response_length) == 0) {
		if (!drop_reply && write_all(STDOUT_FILENO, response, response_length, &deadline) != 0) {
			perror("write stdout");
			goto cleanup;
		}
		result = 0;
	} else if (print_error_reply(state->reply) != 0) {
		fprintf(stderr, "invalid XPC reply shape\n");
	}

cleanup:
	if (connection != NULL) {
		xpc_connection_cancel(connection);
		xpc_release(connection);
	}
	if (request != NULL) {
		xpc_release(request);
	}
	if (callback_finished) {
		release_reply_state(state);
	}
	// On timeout the reply block may still be queued. The process exits next,
	// so retain its small context rather than risk a late callback use-after-free.
	return result;
}
