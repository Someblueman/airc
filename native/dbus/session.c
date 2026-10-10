#include "session.h"
#include "session_filter.h"

#include "../mach/client.h"

#include <stdbool.h>
#include <stdatomic.h>
#include <string.h>
#include <unistd.h>

#define AIRC_DBUS_PATH "/org/airc/Gateway"
#define AIRC_DBUS_AUTH_TIMEOUT_MS 5000U
#define AIRC_DBUS_IDLE_TIMEOUT_MS 30000U
#define AIRC_DBUS_FLUSH_TIMEOUT_MS 3000U

static const char airc_dbus_introspection[] =
    "<node>"
    " <interface name='org.airc.Gateway'>"
    "  <method name='Exchange'>"
    "   <arg type='ay' name='frame' direction='in'/>"
    "   <arg type='ay' name='response' direction='out'/>"
    "  </method>"
    " </interface>"
    "</node>";

struct airc_dbus_session {
    /* Main-context state. A pending auth, worker, flush or close keeps this
     * session alive; only the separate filter state runs on GIO's thread. */
    GIOStream *stream;
    GDBusConnection *connection;
    GDBusAuthObserver *observer;
    GCancellable *auth_cancellable;
    GMainContext *context;
    GSource *auth_timeout;
    GSource *idle_timeout;
    GSource *flush_timeout;
    GCancellable *flush_cancellable;
    const char *upstream_name;
    airc_dbus_filter_state_t *filter_state;
    airc_dbus_session_finished_fn finished_fn;
    gpointer finished_data;
    GThread *worker;
    GDBusMethodInvocation *invocation;
    guint registration_id;
    guint filter_id;
    gulong closed_handler;
    gboolean started;
    gboolean auth_pending;
    gboolean flush_pending;
    gboolean connection_closed;
    gboolean close_pending;
    atomic_bool stopping;
    gboolean finished;
};

struct airc_dbus_work {
    airc_dbus_session_t *session;
    uint8_t request[AIRC_MACH_REQUEST_MAX];
    size_t request_length;
    uint8_t response[AIRC_MACH_RESPONSE_MAX];
    size_t response_length;
    airc_mach_status_t status;
};

static gboolean airc_dbus_allow_mechanism(GDBusAuthObserver *observer,
                                           const char *mechanism,
                                           gpointer user_data)
{
    (void)observer;
    (void)user_data;
    return g_strcmp0(mechanism, "EXTERNAL") == 0;
}

static gboolean airc_dbus_authorize_peer(GDBusAuthObserver *observer,
                                          GIOStream *stream,
                                          GCredentials *credentials,
                                          gpointer user_data)
{
    GError *error = NULL;
    uid_t uid;
    (void)observer;
    (void)stream;
    (void)user_data;

    if (credentials == NULL) {
        return FALSE;
    }
    uid = g_credentials_get_unix_user(credentials, &error);
    if (error != NULL) {
        g_clear_error(&error);
        return FALSE;
    }
    return uid == geteuid();
}

static gboolean airc_dbus_finish_timeout(gpointer data)
{
    airc_dbus_session_t *session = data;
    airc_dbus_session_stop(session);
    return G_SOURCE_REMOVE;
}

static void airc_dbus_clear_source(GSource **source)
{
    if (*source != NULL) {
        g_source_destroy(*source);
        g_source_unref(*source);
        *source = NULL;
    }
}

static void airc_dbus_closed(GDBusConnection *connection,
                             gboolean remote_peer_vanished,
                             GError *error,
                             gpointer user_data);

static gboolean airc_dbus_peer_uid_ok(GDBusConnection *connection)
{
    GCredentials *credentials = g_dbus_connection_get_peer_credentials(connection);
    GError *error = NULL;
    uid_t uid;

    if (credentials == NULL) {
        return FALSE;
    }
    uid = g_credentials_get_unix_user(credentials, &error);
    if (error != NULL) {
        g_clear_error(&error);
        return FALSE;
    }
    return uid == geteuid();
}

static void airc_dbus_finish_session(airc_dbus_session_t *session)
{
    if (session->finished || session->auth_pending || session->flush_pending ||
        session->close_pending ||
        session->worker != NULL ||
        (session->connection != NULL && !session->connection_closed)) {
        return;
    }
    session->finished = TRUE;
    airc_dbus_clear_source(&session->auth_timeout);
    airc_dbus_clear_source(&session->idle_timeout);
    airc_dbus_clear_source(&session->flush_timeout);
    if (session->connection != NULL) {
        if (session->closed_handler != 0U) {
            g_signal_handler_disconnect(session->connection,
                                        session->closed_handler);
            session->closed_handler = 0U;
        }
    }
    if (session->finished_fn != NULL) {
        session->finished_fn(session, session->finished_data);
    }
}

static void airc_dbus_maybe_finish(airc_dbus_session_t *session)
{
    if (session->connection == NULL) {
        session->connection_closed = TRUE;
    }
    airc_dbus_finish_session(session);
}

static void airc_dbus_closed(GDBusConnection *connection,
                             gboolean remote_peer_vanished,
                             GError *error,
                             gpointer user_data)
{
    airc_dbus_session_t *session = user_data;
    (void)connection;
    (void)remote_peer_vanished;
    (void)error;
    session->connection_closed = TRUE;
    airc_dbus_maybe_finish(session);
}

static void airc_dbus_close_finished(GObject *source,
                                     GAsyncResult *result,
                                     gpointer user_data)
{
    airc_dbus_session_t *session = user_data;
    GError *error = NULL;

    if (!g_dbus_connection_close_finish(G_DBUS_CONNECTION(source), result,
                                        &error)) {
        g_clear_error(&error);
    }
    session->close_pending = FALSE;
    session->connection_closed = TRUE;
    airc_dbus_maybe_finish(session);
}

static void airc_dbus_close_session_connection(
    airc_dbus_session_t *session)
{
    if (session->connection == NULL) {
        session->connection_closed = TRUE;
        airc_dbus_maybe_finish(session);
        return;
    }
    if (g_dbus_connection_is_closed(session->connection)) {
        session->connection_closed = TRUE;
        airc_dbus_maybe_finish(session);
        return;
    }
    if (!session->close_pending) {
        session->close_pending = TRUE;
        g_dbus_connection_close(session->connection, NULL,
                                airc_dbus_close_finished, session);
    }
}

static gboolean airc_dbus_cancel_flush(gpointer user_data)
{
    airc_dbus_session_t *session = user_data;
    g_cancellable_cancel(session->flush_cancellable);
    return G_SOURCE_REMOVE;
}

static void airc_dbus_flush_finished(GObject *source,
                                     GAsyncResult *result,
                                     gpointer user_data)
{
    airc_dbus_session_t *session = user_data;
    GError *error = NULL;

    if (!g_dbus_connection_flush_finish(G_DBUS_CONNECTION(source), result,
                                        &error)) {
        g_clear_error(&error);
    }
    session->flush_pending = FALSE;
    airc_dbus_clear_source(&session->flush_timeout);
    g_clear_object(&session->flush_cancellable);
    airc_dbus_close_session_connection(session);
}

static void airc_dbus_flush_reply(airc_dbus_session_t *session)
{
    if (session->connection == NULL ||
        g_dbus_connection_is_closed(session->connection)) {
        airc_dbus_close_session_connection(session);
        return;
    }
    session->flush_cancellable = g_cancellable_new();
    session->flush_timeout = g_timeout_source_new(AIRC_DBUS_FLUSH_TIMEOUT_MS);
    g_source_set_callback(session->flush_timeout, airc_dbus_cancel_flush,
                          session, NULL);
    g_source_attach(session->flush_timeout, session->context);
    session->flush_pending = TRUE;
    g_dbus_connection_flush(session->connection, session->flush_cancellable,
                            airc_dbus_flush_finished, session);
}

static gboolean airc_dbus_send_completion(gpointer data)
{
    struct airc_dbus_work *work = data;
    airc_dbus_session_t *session = work->session;

    if (session->worker != NULL) {
        g_thread_join(session->worker);
        session->worker = NULL;
    }
    if (!atomic_load_explicit(&session->stopping, memory_order_acquire) &&
        session->connection != NULL &&
        !g_dbus_connection_is_closed(session->connection)) {
        if (work->status == AIRC_MACH_OK && work->response_length != 0U &&
            work->response_length <= AIRC_MACH_RESPONSE_MAX) {
            GVariant *array = g_variant_new_fixed_array(
                G_VARIANT_TYPE_BYTE, work->response, work->response_length,
                sizeof(uint8_t));
            g_dbus_method_invocation_return_value(
                session->invocation, g_variant_new("(@ay)", array));
        } else {
            const char *name = work->status == AIRC_MACH_TIMEOUT
                                   ? "org.airc.Gateway.Error.Timeout"
                                   : "org.airc.Gateway.Error.Upstream";
            const char *message = work->status == AIRC_MACH_TIMEOUT
                                      ? "upstream transaction timed out"
                                      : "upstream transaction failed";
            g_dbus_method_invocation_return_dbus_error(
                session->invocation, name, message);
        }
        session->invocation = NULL;
        memset(work, 0, sizeof(*work));
        g_free(work);
        airc_dbus_flush_reply(session);
        return G_SOURCE_REMOVE;
    }
    if (session->invocation != NULL) {
        g_object_unref(session->invocation);
        session->invocation = NULL;
    }
    memset(work, 0, sizeof(*work));
    g_free(work);
    if (session->connection != NULL &&
        !g_dbus_connection_is_closed(session->connection)) {
        airc_dbus_close_session_connection(session);
        return G_SOURCE_REMOVE;
    }
    airc_dbus_maybe_finish(session);
    return G_SOURCE_REMOVE;
}

static void airc_dbus_reply_error(airc_dbus_session_t *session,
                                  GDBusMethodInvocation *invocation,
                                  const char *name,
                                  const char *message)
{
    g_dbus_method_invocation_return_dbus_error(invocation, name, message);
    airc_dbus_flush_reply(session);
}

static gpointer airc_dbus_run_mach(gpointer data)
{
    struct airc_dbus_work *work = data;

    work->status = airc_mach_transact(
        work->session->upstream_name, work->request, work->request_length,
        work->response, sizeof(work->response), &work->response_length);
    {
        GSource *source = g_idle_source_new();
        g_source_set_priority(source, G_PRIORITY_DEFAULT);
        g_source_set_callback(source, airc_dbus_send_completion, work, NULL);
        g_source_attach(source, work->session->context);
        g_source_unref(source);
    }
    return NULL;
}

static void airc_dbus_exchange(GDBusConnection *connection,
                               const char *sender,
                               const char *object_path,
                               const char *interface_name,
                               const char *method_name,
                               GVariant *parameters,
                               GDBusMethodInvocation *invocation,
                               gpointer user_data)
{
    airc_dbus_session_t *session = user_data;
    GDBusMessage *message = g_dbus_method_invocation_get_message(invocation);
    GVariant *array = NULL;
    gsize frame_length = 0U;
    const uint8_t *frame;
    struct airc_dbus_work *work = NULL;
    GError *error = NULL;
    (void)sender;
    (void)object_path;
    (void)interface_name;
    (void)method_name;

    if (atomic_load_explicit(&session->stopping, memory_order_acquire) ||
        g_dbus_connection_is_closed(connection)) {
        g_object_unref(invocation);
        return;
    }
    if (!airc_dbus_peer_uid_ok(connection)) {
        airc_dbus_reply_error(session, invocation,
                              "org.airc.Gateway.Error.Peer",
                              "peer identity refused");
        return;
    }
    if (g_dbus_message_get_num_unix_fds(message) != 0U) {
        airc_dbus_reply_error(session, invocation,
                              "org.airc.Gateway.Error.InvalidRequest",
                              "file descriptors are not accepted");
        return;
    }
    g_variant_get(parameters, "(@ay)", &array);
    frame = g_variant_get_fixed_array(array, &frame_length, sizeof(uint8_t));
    if (frame == NULL || frame_length == 0U ||
        frame_length > AIRC_MACH_REQUEST_MAX) {
        airc_dbus_reply_error(session, invocation,
                              "org.airc.Gateway.Error.InvalidRequest",
                              "frame size is invalid");
        g_variant_unref(array);
        return;
    }
    work = g_new0(struct airc_dbus_work, 1U);
    memcpy(work->request, frame, frame_length);
    work->request_length = frame_length;
    work->session = session;
    session->invocation = invocation;
    session->worker = g_thread_try_new("airc-dbus-mach", airc_dbus_run_mach,
                                       work, &error);
    g_variant_unref(array);
    if (session->worker == NULL) {
        g_clear_error(&error);
        session->invocation = NULL;
        g_free(work);
        airc_dbus_reply_error(session, invocation,
                              "org.airc.Gateway.Error.Unavailable",
                              "upstream worker unavailable");
    }
}

static const GDBusInterfaceVTable airc_dbus_vtable = {
    .method_call = airc_dbus_exchange,
    .get_property = NULL,
    .set_property = NULL,
    .padding = {NULL, NULL, NULL, NULL}
};

static gboolean airc_dbus_auth_timeout(gpointer data)
{
    airc_dbus_session_t *session = data;
    atomic_store_explicit(&session->stopping, true, memory_order_release);
    g_cancellable_cancel(session->auth_cancellable);
    return G_SOURCE_REMOVE;
}

static void airc_dbus_auth_ready(GObject *source,
                                GAsyncResult *result,
                                gpointer user_data)
{
    airc_dbus_session_t *session = user_data;
    GError *error = NULL;
    GDBusNodeInfo *node = NULL;
    GDBusConnection *connection;

    connection = g_dbus_connection_new_finish(result, &error);
    session->auth_pending = FALSE;
    airc_dbus_clear_source(&session->auth_timeout);
    if (connection == NULL) {
        g_clear_error(&error);
        airc_dbus_maybe_finish(session);
        return;
    }
    session->connection = connection;
    g_dbus_connection_set_exit_on_close(connection, FALSE);
    session->closed_handler = g_signal_connect(
        connection, "closed", G_CALLBACK(airc_dbus_closed), session);
    if (g_dbus_connection_is_closed(connection)) {
        session->connection_closed = TRUE;
        airc_dbus_maybe_finish(session);
        return;
    }
    if (atomic_load_explicit(&session->stopping, memory_order_acquire) ||
        !airc_dbus_peer_uid_ok(connection)) {
        atomic_store_explicit(&session->stopping, true, memory_order_release);
        airc_dbus_close_session_connection(session);
        return;
    }
    node = g_dbus_node_info_new_for_xml(airc_dbus_introspection, &error);
    if (node == NULL) {
        g_clear_error(&error);
        atomic_store_explicit(&session->stopping, true, memory_order_release);
        airc_dbus_close_session_connection(session);
        return;
    }
    session->registration_id = g_dbus_connection_register_object(
        connection, AIRC_DBUS_PATH, node->interfaces[0], &airc_dbus_vtable,
        session, NULL, &error);
    g_dbus_node_info_unref(node);
    if (session->registration_id == 0U) {
        g_clear_error(&error);
        atomic_store_explicit(&session->stopping, true, memory_order_release);
        airc_dbus_close_session_connection(session);
        return;
    }
    session->filter_state = airc_dbus_filter_state_new();
    session->filter_id = g_dbus_connection_add_filter(
        connection, airc_dbus_message_filter, session->filter_state,
        airc_dbus_filter_state_free);
    g_dbus_connection_start_message_processing(connection);
    session->idle_timeout = g_timeout_source_new(AIRC_DBUS_IDLE_TIMEOUT_MS);
    g_source_set_callback(session->idle_timeout, airc_dbus_finish_timeout,
                          session, NULL);
    g_source_attach(session->idle_timeout, session->context);
    (void)source;
}

airc_dbus_session_t *airc_dbus_session_new(
    GIOStream *stream, const char *upstream_name, GMainContext *context,
    airc_dbus_session_finished_fn finished_fn, gpointer user_data)
{
    airc_dbus_session_t *session;

    if (!G_IS_IO_STREAM(stream) || upstream_name == NULL || context == NULL ||
        finished_fn == NULL) {
        return NULL;
    }
    session = g_new0(airc_dbus_session_t, 1U);
    session->stream = g_object_ref(stream);
    session->upstream_name = upstream_name;
    session->context = g_main_context_ref(context);
    session->finished_fn = finished_fn;
    session->finished_data = user_data;
    session->observer = g_dbus_auth_observer_new();
    session->auth_cancellable = g_cancellable_new();
    atomic_init(&session->stopping, false);
    g_signal_connect(session->observer, "allow-mechanism",
                     G_CALLBACK(airc_dbus_allow_mechanism), session);
    g_signal_connect(session->observer, "authorize-authenticated-peer",
                     G_CALLBACK(airc_dbus_authorize_peer), session);
    return session;
}

void airc_dbus_session_start(airc_dbus_session_t *session)
{
    char *guid;

    if (session == NULL || session->started || session->finished) {
        return;
    }
    session->started = TRUE;
    session->auth_pending = TRUE;
    session->auth_timeout = g_timeout_source_new(AIRC_DBUS_AUTH_TIMEOUT_MS);
    g_source_set_callback(session->auth_timeout, airc_dbus_auth_timeout,
                          session, NULL);
    g_source_attach(session->auth_timeout, session->context);
    guid = g_dbus_generate_guid();
    g_dbus_connection_new(session->stream, guid,
                          G_DBUS_CONNECTION_FLAGS_AUTHENTICATION_SERVER |
                              G_DBUS_CONNECTION_FLAGS_DELAY_MESSAGE_PROCESSING,
                          session->observer, session->auth_cancellable,
                          airc_dbus_auth_ready, session);
    g_free(guid);
}

void airc_dbus_session_stop(airc_dbus_session_t *session)
{
    if (session == NULL || session->finished) {
        return;
    }
    atomic_store_explicit(&session->stopping, true, memory_order_release);
    if (session->auth_cancellable != NULL) {
        g_cancellable_cancel(session->auth_cancellable);
    }
    if (session->flush_cancellable != NULL) {
        g_cancellable_cancel(session->flush_cancellable);
    }
    if (session->connection != NULL) {
        airc_dbus_close_session_connection(session);
        return;
    }
    if (!session->auth_pending && session->worker == NULL &&
        session->connection == NULL) {
        airc_dbus_maybe_finish(session);
    }
}

void airc_dbus_session_free(airc_dbus_session_t *session)
{
    if (session == NULL) {
        return;
    }
    airc_dbus_clear_source(&session->auth_timeout);
    airc_dbus_clear_source(&session->idle_timeout);
    if (session->worker != NULL) {
        g_thread_join(session->worker);
        session->worker = NULL;
    }
    if (session->connection != NULL) {
        if (session->filter_id != 0U) {
            g_dbus_connection_remove_filter(session->connection,
                                            session->filter_id);
        }
        if (session->registration_id != 0U) {
            g_dbus_connection_unregister_object(session->connection,
                                                session->registration_id);
        }
        session->connection_closed = TRUE;
        g_object_unref(session->connection);
    }
    if (session->filter_state != NULL && session->filter_id == 0U) {
        airc_dbus_filter_state_free(session->filter_state);
    }
    if (session->stream != NULL) {
        g_object_unref(session->stream);
    }
    g_clear_object(&session->observer);
    g_clear_object(&session->auth_cancellable);
    g_clear_object(&session->flush_cancellable);
    if (session->context != NULL) {
        g_main_context_unref(session->context);
    }
    g_free(session);
}
