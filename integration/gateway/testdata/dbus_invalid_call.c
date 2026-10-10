#include <gio/gio.h>
#include <gio/gunixfdlist.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static GVariant *make_parameters(const char *mode)
{
	if (strcmp(mode, "wrong-signature") == 0) {
		return g_variant_new("(s)", "not-an-ay");
	}
	if (strcmp(mode, "missing-argument") == 0) {
		return g_variant_new("()");
	}
	if (strcmp(mode, "extra-argument") == 0) {
		static const guint8 frame[] = "{}";
		GVariant *bytes = g_variant_new_fixed_array(G_VARIANT_TYPE_BYTE, frame, sizeof(frame) - 1U, sizeof(frame[0]));
		return g_variant_new("(@ay&s)", bytes, "unexpected");
	}
	if (strcmp(mode, "oversized-argument") == 0) {
		const gsize length = (gsize)16U * 1024U + 1U;
		guint8 *frame = g_malloc0(length);
		GVariant *bytes = g_variant_new_fixed_array(G_VARIANT_TYPE_BYTE, frame, length, sizeof(*frame));
		g_free(frame);
		return g_variant_new("(@ay)", bytes);
	}
	if (strcmp(mode, "fd-transfer") == 0) {
		static const guint8 frame[] = "{}";
		GVariant *bytes = g_variant_new_fixed_array(G_VARIANT_TYPE_BYTE, frame, sizeof(frame) - 1U, sizeof(frame[0]));
		return g_variant_new("(@ay)", bytes);
	}
	return NULL;
}

int main(int argc, char **argv)
{
	if (argc != 3) {
		fprintf(stderr, "usage: %s unix-socket mode\n", argv[0]);
		return 2;
	}
	char *address = g_strdup_printf("unix:path=%s", argv[1]);
	GError *error = NULL;
	GDBusConnection *connection = g_dbus_connection_new_for_address_sync(
		address, G_DBUS_CONNECTION_FLAGS_AUTHENTICATION_CLIENT, NULL, NULL, &error);
	g_free(address);
	if (connection == NULL) {
		fprintf(stderr, "connect peer D-Bus: %s\n", error == NULL ? "unknown error" : error->message);
		g_clear_error(&error);
		return 1;
	}
	GVariant *parameters = make_parameters(argv[2]);
	if (parameters == NULL) {
		fprintf(stderr, "unknown probe mode\n");
		g_object_unref(connection);
		return 2;
	}
	parameters = g_variant_ref_sink(parameters);
	GVariant *reply = NULL;
	if (strcmp(argv[2], "fd-transfer") == 0) {
		GUnixFDList *fds = g_unix_fd_list_new();
		int fd = open("/dev/null", O_RDONLY);
		if (fd < 0 || g_unix_fd_list_append(fds, fd, &error) < 0) {
			fprintf(stderr, "prepare fd transfer: %s\n", error == NULL ? "open failed" : error->message);
			g_clear_error(&error);
			if (fd >= 0) (void)close(fd);
			g_object_unref(fds);
			g_variant_unref(parameters);
			g_object_unref(connection);
			return 1;
		}
		(void)close(fd);
		if ((g_dbus_connection_get_capabilities(connection) & G_DBUS_CAPABILITY_FLAGS_UNIX_FD_PASSING) == 0) {
			fputs("fd-passing-unsupported\n", stdout);
			g_object_unref(fds);
			g_variant_unref(parameters);
			g_object_unref(connection);
			return 77;
		}
		reply = g_dbus_connection_call_with_unix_fd_list_sync(connection, NULL,
			"/org/airc/Gateway", "org.airc.Gateway", "Exchange", parameters,
			G_VARIANT_TYPE("(ay)"), G_DBUS_CALL_FLAGS_NONE, 5000, fds, NULL, NULL, &error);
		g_object_unref(fds);
	} else {
		reply = g_dbus_connection_call_sync(connection, NULL,
			"/org/airc/Gateway", "org.airc.Gateway", "Exchange", parameters,
			G_VARIANT_TYPE("(ay)"), G_DBUS_CALL_FLAGS_NONE, 5000, NULL, &error);
	}
	g_variant_unref(parameters);
	int result = 1;
	if (reply != NULL) {
		fputs("malformed call unexpectedly succeeded\n", stderr);
		g_variant_unref(reply);
	} else if (error == NULL || !g_dbus_error_is_remote_error(error) ||
		strlen(error->message) == 0 || strlen(error->message) > 1024) {
		fprintf(stderr, "malformed call did not receive a bounded D-Bus error: %s\n",
			error == NULL ? "(null)" : error->message);
	} else {
		fputs("rejected\n", stdout);
		result = 0;
	}
	g_clear_error(&error);
	GError *close_error = NULL;
	if (!g_dbus_connection_close_sync(connection, NULL, &close_error)) {
		if (result == 0 && !g_error_matches(close_error, G_IO_ERROR, G_IO_ERROR_CLOSED)) {
			fprintf(stderr, "close peer D-Bus: %s\n", close_error->message);
			result = 1;
		}
		g_clear_error(&close_error);
	}
	g_object_unref(connection);
	return result;
}
