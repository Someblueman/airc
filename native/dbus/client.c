#include <gio/gio.h>
#include <errno.h>
#include <poll.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/un.h>
#include <time.h>
#include <unistd.h>

enum {
	FRAME_LIMIT = 16 * 1024,
	RESPONSE_LIMIT = 64 * 1024,
	WAIT_SECONDS = 8
};

static void terminate_on_alarm(int signal_number) {
	(void)signal_number;
	_exit(124);
}

static int install_deadline(void) {
	struct sigaction action;
	memset(&action, 0, sizeof(action));
	action.sa_handler = terminate_on_alarm;
	if (sigemptyset(&action.sa_mask) != 0 || sigaction(SIGALRM, &action, NULL) != 0) {
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
	*length = 0;
	for (;;) {
		int timeout = remaining_milliseconds(deadline);
		if (timeout <= 0) {
			fprintf(stderr, "timed out reading request from stdin\n");
			return -1;
		}
		struct pollfd input = {.fd = STDIN_FILENO, .events = POLLIN};
		int ready = poll(&input, 1, timeout);
		if (ready < 0 && errno == EINTR) {
			continue;
		}
		if (ready < 0) {
			perror("poll stdin");
			return -1;
		}
		if (ready == 0) {
			fprintf(stderr, "timed out reading request from stdin\n");
			return -1;
		}
		if ((input.revents & (POLLERR | POLLNVAL)) != 0) {
			fprintf(stderr, "stdin is not readable\n");
			return -1;
		}
		if (*length == FRAME_LIMIT + 1) {
			fprintf(stderr, "request exceeds %d bytes\n", FRAME_LIMIT);
			return -1;
		}
		size_t available = FRAME_LIMIT + 1 - *length;
		ssize_t count = read(STDIN_FILENO, frame + *length, available);
		if (count < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK)) {
			continue;
		}
		if (count < 0) {
			perror("read stdin");
			return -1;
		}
		if (count == 0) {
			if (*length == 0) {
				fprintf(stderr, "request stdin is empty\n");
				return -1;
			}
			return 0;
		}
		*length += (size_t)count;
		if (*length > FRAME_LIMIT) {
			fprintf(stderr, "request exceeds %d bytes\n", FRAME_LIMIT);
			return -1;
		}
	}
}

static int write_all(const uint8_t *data, size_t length,
		const struct timespec *deadline) {
	size_t written = 0;
	while (written < length) {
		int timeout = remaining_milliseconds(deadline);
		if (timeout <= 0) {
			errno = ETIMEDOUT;
			return -1;
		}
		struct pollfd output = {.fd = STDOUT_FILENO, .events = POLLOUT};
		int ready = poll(&output, 1, timeout);
		if (ready < 0 && errno == EINTR) {
			continue;
		}
		if (ready < 0) {
			return -1;
		}
		if (ready == 0) {
			errno = ETIMEDOUT;
			return -1;
		}
		if ((output.revents & (POLLERR | POLLNVAL)) != 0) {
			errno = EIO;
			return -1;
		}
		ssize_t count = write(STDOUT_FILENO, data + written, length - written);
		if (count < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK)) {
			continue;
		}
		if (count <= 0) {
			return -1;
		}
		written += (size_t)count;
	}
	return 0;
}

static gboolean allow_external_only(GDBusAuthObserver *observer,
		const gchar *mechanism, gpointer user_data) {
	(void)observer;
	(void)user_data;
	return strcmp(mechanism, "EXTERNAL") == 0;
}

static char *unix_address(const char *socket_path) {
	if (socket_path[0] != '/' ||
	    strlen(socket_path) >= sizeof(((struct sockaddr_un *)0)->sun_path)) {
		return NULL;
	}
	char *escaped = g_dbus_address_escape_value(socket_path);
	if (escaped == NULL) {
		return NULL;
	}
	char *address = g_strconcat("unix:path=", escaped, NULL);
	g_free(escaped);
	return address;
}

int main(int argc, char **argv) {
	const char *socket_path = NULL;
	gboolean drop_reply = FALSE;
	if (argc == 3 && strcmp(argv[1], "--socket") == 0) {
		socket_path = argv[2];
	} else if (argc == 4 && strcmp(argv[1], "--socket") == 0 &&
			strcmp(argv[3], "--drop-reply") == 0) {
		socket_path = argv[2];
		drop_reply = TRUE;
	} else {
		fprintf(stderr, "usage: %s --socket ABS_PATH [--drop-reply]\n", argv[0]);
		return 2;
	}
	if (install_deadline() != 0) {
		perror("install deadline");
		return 1;
	}
	char *address = unix_address(socket_path);
	if (address == NULL) {
		fprintf(stderr, "socket path must be absolute and fit in sockaddr_un\n");
		return 2;
	}
	struct timespec deadline;
	if (clock_gettime(CLOCK_MONOTONIC, &deadline) != 0) {
		perror("clock_gettime");
		g_free(address);
		return 1;
	}
	deadline.tv_sec += WAIT_SECONDS;
	uint8_t frame[FRAME_LIMIT + 1];
	size_t frame_length = 0;
	if (read_frame(frame, &frame_length, &deadline) != 0) {
		g_free(address);
		return 1;
	}

	GDBusAuthObserver *observer = NULL;
	GDBusConnection *connection = NULL;
	GUnixFDList *received_fds = NULL;
	GVariant *parameters = NULL;
	GVariant *reply = NULL;
	GVariant *array = NULL;
	GError *error = NULL;
	int result = 1;
	const guint8 *response = NULL;
	gsize response_length = 0;

	observer = g_dbus_auth_observer_new();
	if (observer == NULL) {
		fprintf(stderr, "could not create D-Bus auth observer\n");
		goto cleanup;
	}
	g_signal_connect(observer, "allow-mechanism", G_CALLBACK(allow_external_only), NULL);
	connection = g_dbus_connection_new_for_address_sync(address,
			G_DBUS_CONNECTION_FLAGS_AUTHENTICATION_CLIENT,
			observer, NULL, &error);
	if (connection == NULL) {
		fprintf(stderr, "D-Bus peer connection failed\n");
		goto cleanup;
	}
	g_dbus_connection_set_exit_on_close(connection, FALSE);
	GVariant *bytes = g_variant_new_fixed_array(G_VARIANT_TYPE_BYTE,
			frame, frame_length, sizeof(frame[0]));
	parameters = g_variant_ref_sink(g_variant_new("(@ay)", bytes));
	int timeout = remaining_milliseconds(&deadline);
	if (timeout <= 0) {
		fprintf(stderr, "D-Bus request timed out\n");
		goto cleanup;
	}
	reply = g_dbus_connection_call_with_unix_fd_list_sync(connection, NULL,
			"/org/airc/Gateway", "org.airc.Gateway", "Exchange",
			parameters, G_VARIANT_TYPE("(ay)"), G_DBUS_CALL_FLAGS_NONE,
		timeout, NULL, &received_fds, NULL, &error);
	if (reply == NULL) {
		fprintf(stderr, "D-Bus exchange failed\n");
		goto cleanup;
	}
	if (received_fds != NULL && g_unix_fd_list_get_length(received_fds) != 0) {
		fprintf(stderr, "D-Bus response included unexpected file descriptors\n");
		goto cleanup;
	}
	g_variant_get(reply, "(@ay)", &array);
	response = g_variant_get_fixed_array(array, &response_length, sizeof(guint8));
	if (response == NULL || response_length == 0 || response_length > RESPONSE_LIMIT) {
		fprintf(stderr, "D-Bus response size refused\n");
		goto cleanup;
	}
	if (!drop_reply && write_all(response, response_length, &deadline) != 0) {
		perror("write stdout");
		goto cleanup;
	}
	result = 0;

cleanup:
	if (error != NULL) {
		g_error_free(error);
	}
	if (received_fds != NULL) {
		g_object_unref(received_fds);
	}
	if (array != NULL) {
		g_variant_unref(array);
	}
	if (reply != NULL) {
		g_variant_unref(reply);
	}
	if (parameters != NULL) {
		g_variant_unref(parameters);
	}
	if (connection != NULL) {
		g_dbus_connection_close_sync(connection, NULL, NULL);
		g_object_unref(connection);
	}
	if (observer != NULL) {
		g_object_unref(observer);
	}
	g_free(address);
	return result;
}
