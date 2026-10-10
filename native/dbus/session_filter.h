#ifndef AIRC_DBUS_SESSION_FILTER_H
#define AIRC_DBUS_SESSION_FILTER_H

#include <gio/gio.h>

typedef struct airc_dbus_filter_state airc_dbus_filter_state_t;

/* The caller owns this state until it passes it to add_filter with
 * airc_dbus_filter_state_free as the destroy callback. The filter consumes
 * incoming messages it drops and returns all messages it retains. */
airc_dbus_filter_state_t *airc_dbus_filter_state_new(void);
void airc_dbus_filter_state_free(gpointer state);
GDBusMessage *airc_dbus_message_filter(GDBusConnection *connection,
                                        GDBusMessage *message,
                                        gboolean incoming,
                                        gpointer user_data);

#endif
