#include "session_filter.h"

#include <stdbool.h>
#include <stdatomic.h>

struct airc_dbus_filter_state {
    atomic_bool request_claimed;
};

airc_dbus_filter_state_t *airc_dbus_filter_state_new(void)
{
    airc_dbus_filter_state_t *state;

    state = g_new0(airc_dbus_filter_state_t, 1U);
    atomic_init(&state->request_claimed, false);
    return state;
}

void airc_dbus_filter_state_free(gpointer data)
{
    g_free(data);
}

GDBusMessage *airc_dbus_message_filter(GDBusConnection *connection,
                                        GDBusMessage *message,
                                        gboolean incoming,
                                        gpointer user_data)
{
    airc_dbus_filter_state_t *state = user_data;
    bool expected = false;
    (void)connection;

    if (!incoming ||
        (g_dbus_message_get_message_type(message) ==
             G_DBUS_MESSAGE_TYPE_METHOD_CALL &&
         atomic_compare_exchange_strong_explicit(
             &state->request_claimed, &expected, true,
             memory_order_acq_rel, memory_order_acquire))) {
        return message;
    }
    g_object_unref(message);
    return NULL;
}
