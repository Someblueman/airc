#include "session.h"
#include "../mach/client.h"

#include <gio/gio.h>
#include <glib-unix.h>
#include <errno.h>
#include <limits.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>

#define AIRC_DBUS_BACKLOG 4
#define AIRC_DBUS_READY_LINE "AIRC_DBUS_READY"

struct airc_dbus_server {
    GMainLoop *loop;
    GSocketListener *listener;
    GCancellable *accept_cancellable;
    gboolean accept_pending;
    airc_dbus_session_t *active;
    GMainContext *context;
    const char *upstream_name;
    const char *socket_path;
    struct stat socket_identity;
    gboolean socket_owned;
    gboolean stopping;
    guint sigint_source;
    guint sigterm_source;
    int result;
};

static gboolean airc_path_is_private_directory(const char *path)
{
    char resolved[PATH_MAX];
    char parent[PATH_MAX];
    const char *slash;
    size_t parent_length;
    struct stat info;

    if (path == NULL || path[0] != '/') {
        return FALSE;
    }
    slash = strrchr(path, '/');
    if (slash == NULL || slash[1] == '\0') {
        return FALSE;
    }
    parent_length = (size_t)(slash - path);
    if (parent_length == 0U) {
        parent_length = 1U;
    }
    if (parent_length >= sizeof(parent)) {
        return FALSE;
    }
    memcpy(parent, path, parent_length);
    parent[parent_length] = '\0';
    if (realpath(parent, resolved) == NULL || strcmp(parent, resolved) != 0 ||
        lstat(parent, &info) != 0 || !S_ISDIR(info.st_mode) ||
        S_ISLNK(info.st_mode) || info.st_uid != geteuid() ||
        (info.st_mode & (S_IRWXG | S_IRWXO)) != 0) {
        return FALSE;
    }
    return strlen(path) <= sizeof(((struct sockaddr_un *)0)->sun_path) - 1U;
}

static void airc_dbus_maybe_stop(struct airc_dbus_server *server)
{
    if (server->stopping && !server->accept_pending && server->active == NULL &&
        server->loop != NULL) {
        g_main_loop_quit(server->loop);
    }
}

static void airc_dbus_session_finished(airc_dbus_session_t *session,
                                       gpointer user_data)
{
    struct airc_dbus_server *server = user_data;

    if (server->active != session) {
        return;
    }
    server->active = NULL;
    airc_dbus_session_free(session);
    airc_dbus_maybe_stop(server);
}

static void airc_dbus_accept_next(struct airc_dbus_server *server);
static gboolean airc_dbus_stop_signal(gpointer user_data);

static void airc_dbus_accepted(GObject *source, GAsyncResult *result,
                              gpointer user_data)
{
    struct airc_dbus_server *server = user_data;
    GError *error = NULL;
    GSocketConnection *connection = g_socket_listener_accept_finish(
        G_SOCKET_LISTENER(source), result, NULL, &error);

    server->accept_pending = FALSE;
    if (server->stopping) {
        g_clear_object(&connection);
        g_clear_error(&error);
        /* The canceled accept's source is gone before its FD is closed. */
        g_socket_listener_close(server->listener);
        airc_dbus_maybe_stop(server);
        return;
    }
    if (connection == NULL) {
        fprintf(stderr, "unable to accept D-Bus peer: %s\n",
                error == NULL ? "unknown error" : error->message);
        g_clear_error(&error);
        server->result = 1;
        (void)airc_dbus_stop_signal(server);
        return;
    }
    if (server->active != NULL) {
        g_io_stream_close(G_IO_STREAM(connection), NULL, NULL);
    } else {
        server->active = airc_dbus_session_new(
            G_IO_STREAM(connection), server->upstream_name, server->context,
            airc_dbus_session_finished, server);
        if (server->active != NULL) {
            airc_dbus_session_start(server->active);
        }
    }
    g_object_unref(connection);
    airc_dbus_accept_next(server);
}

static void airc_dbus_accept_next(struct airc_dbus_server *server)
{
    server->accept_pending = TRUE;
    g_socket_listener_accept_async(server->listener, server->accept_cancellable,
                                    airc_dbus_accepted, server);
}

static gboolean airc_dbus_stop_signal(gpointer user_data)
{
    struct airc_dbus_server *server = user_data;

    if (server->stopping) {
        return G_SOURCE_REMOVE;
    }
    server->stopping = TRUE;
    g_cancellable_cancel(server->accept_cancellable);
    if (!server->accept_pending) {
        g_socket_listener_close(server->listener);
    }
    if (server->active != NULL) {
        airc_dbus_session_stop(server->active);
    }
    airc_dbus_maybe_stop(server);
    return G_SOURCE_REMOVE;
}

static gboolean airc_dbus_sigint(gpointer user_data)
{
    struct airc_dbus_server *server = user_data;
    server->sigint_source = 0U;
    if (server->sigterm_source != 0U) {
        g_source_remove(server->sigterm_source);
        server->sigterm_source = 0U;
    }
    return airc_dbus_stop_signal(user_data);
}

static gboolean airc_dbus_sigterm(gpointer user_data)
{
    struct airc_dbus_server *server = user_data;
    server->sigterm_source = 0U;
    if (server->sigint_source != 0U) {
        g_source_remove(server->sigint_source);
        server->sigint_source = 0U;
    }
    return airc_dbus_stop_signal(user_data);
}

static void airc_dbus_unlink_owned_socket(struct airc_dbus_server *server)
{
    struct stat current;

    if (!server->socket_owned ||
        lstat(server->socket_path, &current) != 0 ||
        !S_ISSOCK(current.st_mode) ||
        current.st_dev != server->socket_identity.st_dev ||
        current.st_ino != server->socket_identity.st_ino) {
        return;
    }
    (void)unlink(server->socket_path);
    server->socket_owned = FALSE;
}

static int airc_dbus_server_run(const char *socket_path,
                         const char *mach_service_name)
{
    struct airc_dbus_server server = {0};
    GSocketAddress *address = NULL;
    GSocketAddress *effective_address = NULL;
    GError *error = NULL;
    mode_t old_mask;
    struct stat existing;
    int result = 1;

    if (!airc_path_is_private_directory(socket_path) ||
        !airc_mach_name_valid(mach_service_name)) {
        fputs("invalid private socket path or Mach service name\n", stderr);
        return 2;
    }
    if (lstat(socket_path, &existing) == 0 || errno != ENOENT) {
        fputs("socket path already exists or cannot be inspected\n", stderr);
        return 2;
    }
    server.socket_path = socket_path;
    server.upstream_name = mach_service_name;
    server.context = g_main_context_default();
    server.loop = g_main_loop_new(server.context, FALSE);
    server.listener = g_socket_listener_new();
    server.accept_cancellable = g_cancellable_new();
    if (server.loop == NULL || server.listener == NULL) {
        fputs("unable to initialize D-Bus service\n", stderr);
        goto cleanup;
    }
    g_socket_listener_set_backlog(server.listener, AIRC_DBUS_BACKLOG);
    address = g_unix_socket_address_new(socket_path);
    old_mask = umask(0077);
    if (!g_socket_listener_add_address(server.listener,
                                       address, G_SOCKET_TYPE_STREAM,
                                       G_SOCKET_PROTOCOL_DEFAULT, NULL,
                                       &effective_address, &error)) {
        umask(old_mask);
        fprintf(stderr, "unable to bind private D-Bus socket: %s\n",
                error == NULL ? "unknown error" : error->message);
        g_clear_error(&error);
        goto cleanup;
    }
    umask(old_mask);
    if (lstat(socket_path, &server.socket_identity) != 0 ||
        !S_ISSOCK(server.socket_identity.st_mode) ||
        server.socket_identity.st_uid != geteuid()) {
        fputs("unable to secure bound D-Bus socket\n", stderr);
        goto cleanup;
    }
    server.socket_owned = TRUE;
    if (chmod(socket_path, 0600) != 0) {
        fputs("unable to secure bound D-Bus socket\n", stderr);
        goto cleanup;
    }
    airc_dbus_accept_next(&server);
    server.sigint_source = g_unix_signal_add(SIGINT, airc_dbus_sigint,
                                             &server);
    server.sigterm_source = g_unix_signal_add(SIGTERM, airc_dbus_sigterm,
                                              &server);
    puts(AIRC_DBUS_READY_LINE);
    fflush(stdout);
    g_main_loop_run(server.loop);
    result = server.result;

cleanup:
    if (!server.stopping && server.listener != NULL) {
        (void)airc_dbus_stop_signal(&server);
    }
    while (server.active != NULL || server.accept_pending) {
        g_main_context_iteration(server.context, TRUE);
    }
    if (server.sigint_source != 0U) {
        g_source_remove(server.sigint_source);
    }
    if (server.sigterm_source != 0U) {
        g_source_remove(server.sigterm_source);
    }
    airc_dbus_unlink_owned_socket(&server);
    g_clear_object(&effective_address);
    g_clear_object(&address);
    g_clear_object(&server.listener);
    g_clear_object(&server.accept_cancellable);
    if (server.loop != NULL) {
        g_main_loop_unref(server.loop);
    }
    return result;
}

static int airc_dbus_usage(const char *program)
{
    fprintf(stderr, "usage: %s --socket ABS_PATH --mach-service NAME\n",
            program);
    return 2;
}

int main(int argc, char **argv)
{
    const char *socket_path = NULL;
    const char *mach_service_name = NULL;
    int i;

    for (i = 1; i < argc; ++i) {
        if (strcmp(argv[i], "--socket") == 0 && i + 1 < argc &&
            socket_path == NULL) {
            socket_path = argv[++i];
        } else if (strcmp(argv[i], "--mach-service") == 0 && i + 1 < argc &&
                   mach_service_name == NULL) {
            mach_service_name = argv[++i];
        } else {
            return airc_dbus_usage(argv[0]);
        }
    }
    if (socket_path == NULL || mach_service_name == NULL) {
        return airc_dbus_usage(argv[0]);
    }
    return airc_dbus_server_run(socket_path, mach_service_name);
}
