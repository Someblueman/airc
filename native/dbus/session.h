#ifndef AIRC_DBUS_SESSION_H
#define AIRC_DBUS_SESSION_H

#include <gio/gio.h>

typedef struct airc_dbus_session airc_dbus_session_t;
typedef void (*airc_dbus_session_finished_fn)(airc_dbus_session_t *session,
                                               gpointer user_data);

/* Takes a reference to stream. The caller owns the returned session until
 * finished_fn runs, then must call airc_dbus_session_free(). */
airc_dbus_session_t *airc_dbus_session_new(
    GIOStream *stream, const char *upstream_name, GMainContext *context,
    airc_dbus_session_finished_fn finished_fn, gpointer user_data);
void airc_dbus_session_start(airc_dbus_session_t *session);
void airc_dbus_session_stop(airc_dbus_session_t *session);
void airc_dbus_session_free(airc_dbus_session_t *session);

#endif
