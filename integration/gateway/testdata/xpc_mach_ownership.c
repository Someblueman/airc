#include "mach.h"

#include <mach/mach.h>
#include <mach/message.h>
#include <servers/bootstrap.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <mach/vm_map.h>

struct simple_message {
	mach_msg_header_t header;
};

struct port_snapshot {
	mach_port_name_array_t names;
	mach_port_type_array_t types;
	mach_msg_type_number_t name_count;
	mach_msg_type_number_t type_count;
};

static int snapshot_ports(struct port_snapshot *snapshot)
{
	kern_return_t result = mach_port_names(mach_task_self(), &snapshot->names,
		&snapshot->name_count, &snapshot->types, &snapshot->type_count);
	if (result != KERN_SUCCESS) {
		fprintf(stderr, "mach_port_names failed: %d\n", result);
		return -1;
	}
	if (snapshot->name_count != snapshot->type_count) {
		fprintf(stderr, "mach_port_names returned mismatched arrays\n");
		return -1;
	}
	return 0;
}

static void release_snapshot(struct port_snapshot *snapshot)
{
	if (snapshot->names != NULL) {
		(void)vm_deallocate(mach_task_self(), (vm_address_t)snapshot->names,
			(vm_size_t)(snapshot->name_count * sizeof(snapshot->names[0])));
	}
	if (snapshot->types != NULL) {
		(void)vm_deallocate(mach_task_self(), (vm_address_t)snapshot->types,
			(vm_size_t)(snapshot->type_count * sizeof(snapshot->types[0])));
	}
	memset(snapshot, 0, sizeof(*snapshot));
}

static int port_was_present(const struct port_snapshot *snapshot,
		mach_port_name_t name)
{
	mach_msg_type_number_t i;
	for (i = 0; i < snapshot->name_count; ++i) {
		if (snapshot->names[i] == name) {
			return 1;
		}
	}
	return 0;
}

static int port_refs(mach_port_t port, mach_port_right_t right, mach_port_urefs_t *refs)
{
	kern_return_t result = mach_port_get_refs(mach_task_self(), port, right, refs);
	if (result != KERN_SUCCESS) {
		fprintf(stderr, "mach_port_get_refs(%u) failed: %d\n", right, result);
		return -1;
	}
	return 0;
}

int main(int argc, char **argv)
{
	char service_name[BOOTSTRAP_MAX_NAME_LEN];
	mach_port_t service = MACH_PORT_NULL;
	mach_port_t service_send = MACH_PORT_NULL;
	struct port_snapshot before = {0};
	struct port_snapshot after = {0};
	mach_port_limits_t limits = {.mpl_qlimit = 1};
	struct simple_message filler = {0};
	uint8_t frame = 0x41;
	uint8_t response[AIRC_MACH_RESPONSE_MAX];
	size_t response_length = 0;
	mach_port_urefs_t send_before = 0;
	mach_port_urefs_t send_after = 0;
	mach_port_urefs_t receive_before = 0;
	mach_port_urefs_t receive_after = 0;
	mach_msg_return_t message_result;
	kern_return_t result;
	int exit_status = EXIT_FAILURE;
	mach_msg_type_number_t i;
	mach_msg_type_number_t unexpected_count = 0;

	if (argc != 2 || strlen(argv[1]) == 0 || strlen(argv[1]) >= sizeof(service_name)) {
		fprintf(stderr, "usage: %s BOOTSTRAP_NAME\n", argv[0]);
		return 2;
	}
	memcpy(service_name, argv[1], strlen(argv[1]) + 1U);
	result = mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &service);
	if (result != KERN_SUCCESS) {
		fprintf(stderr, "allocate service port failed: %d\n", result);
		goto cleanup;
	}
	result = mach_port_insert_right(mach_task_self(), service, service,
		MACH_MSG_TYPE_MAKE_SEND);
	if (result != KERN_SUCCESS) {
		fprintf(stderr, "insert service send right failed: %d\n", result);
		goto cleanup;
	}
	service_send = service;
	result = mach_port_set_attributes(mach_task_self(), service, MACH_PORT_LIMITS_INFO,
		(mach_port_info_t)&limits, MACH_PORT_LIMITS_INFO_COUNT);
	if (result != KERN_SUCCESS) {
		fprintf(stderr, "set queue limit failed: %d\n", result);
		goto cleanup;
	}
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	result = bootstrap_register(bootstrap_port, service_name, service);
#pragma clang diagnostic pop
	if (result != KERN_SUCCESS) {
		fprintf(stderr, "register upstream service failed: %d\n", result);
		goto cleanup;
	}

	/* One queued message fills the port's one-message receive queue. */
	filler.header.msgh_bits = MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND, 0);
	filler.header.msgh_size = sizeof(filler.header);
	filler.header.msgh_remote_port = service;
	filler.header.msgh_local_port = MACH_PORT_NULL;
	filler.header.msgh_voucher_port = MACH_PORT_NULL;
	filler.header.msgh_id = 0x41495200;
	message_result = mach_msg(&filler.header, MACH_SEND_MSG,
		filler.header.msgh_size, 0, MACH_PORT_NULL, MACH_MSG_TIMEOUT_NONE,
		MACH_PORT_NULL);
	if (message_result != MACH_MSG_SUCCESS) {
		fprintf(stderr, "fill service queue failed: %d\n", message_result);
		goto cleanup;
	}
	if (port_refs(service, MACH_PORT_RIGHT_SEND, &send_before) != 0 ||
		port_refs(service, MACH_PORT_RIGHT_RECEIVE, &receive_before) != 0 ||
		snapshot_ports(&before) != 0) {
		goto cleanup;
	}

	airc_mach_status_t status = airc_mach_transact(service_name, &frame, sizeof(frame),
		response, sizeof(response), &response_length);
	if (status != AIRC_MACH_TIMEOUT) {
		fprintf(stderr, "full-queue transaction status=%d, want timeout\n", status);
		goto cleanup;
	}
	if (port_refs(service, MACH_PORT_RIGHT_SEND, &send_after) != 0 ||
		port_refs(service, MACH_PORT_RIGHT_RECEIVE, &receive_after) != 0 ||
		snapshot_ports(&after) != 0) {
		goto cleanup;
	}
	for (i = 0; i < after.name_count; ++i) {
		if (!port_was_present(&before, after.names[i])) {
			fprintf(stderr, "new port name after failed send: name=%u type=0x%x\n",
				after.names[i], after.types[i]);
			++unexpected_count;
		}
	}
	if (before.name_count != after.name_count || unexpected_count != 0 ||
		send_before != send_after || receive_before != receive_after) {
		fprintf(stderr, "rights changed after failed send: names=%u/%u send=%u/%u receive=%u/%u\n",
			before.name_count, after.name_count, send_before, send_after,
			receive_before, receive_after);
		goto cleanup;
	}
	(void)puts("timeout; service refs and port-name set unchanged");
	exit_status = EXIT_SUCCESS;

cleanup:
	release_snapshot(&before);
	release_snapshot(&after);
	if (service_send != MACH_PORT_NULL) {
		(void)mach_port_deallocate(mach_task_self(), service_send);
	}
	if (service != MACH_PORT_NULL) {
		(void)mach_port_mod_refs(mach_task_self(), service,
			MACH_PORT_RIGHT_RECEIVE, -1);
	}
	return exit_status;
}
