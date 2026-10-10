/* Native lifecycle witness: receive a bridge request and hold its reply right
 * until the test closes stdin. No request contents are printed or interpreted. */
#include <mach/mach.h>
#include <mach/message.h>
#include <servers/bootstrap.h>
#include <errno.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static void deadline(int signal_number)
{
	(void)signal_number;
	_exit(124);
}

int main(int argc, char **argv)
{
	mach_port_t service = MACH_PORT_NULL;
	struct {
		mach_msg_header_t header;
		uint8_t body[4 + 16 * 1024 + 3];
		mach_msg_max_trailer_t trailer;
	} message = {0};
	struct sigaction action = {0};
	kern_return_t result;
	int received = 0;
	int send_owned = 0;
	int status = EXIT_FAILURE;
	char control;
	ssize_t count;

	if (argc != 2 || argv[1][0] == '\0' || strlen(argv[1]) >= 128U) {
		fprintf(stderr, "usage: %s UNIQUE_BOOTSTRAP_NAME\n", argv[0]);
		return 2;
	}
	action.sa_handler = deadline;
	if (sigemptyset(&action.sa_mask) != 0 || sigaction(SIGALRM, &action, NULL) != 0) {
		perror("install helper deadline");
		return 1;
	}
	(void)alarm(15);
	result = mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &service);
	if (result != KERN_SUCCESS) {
		fprintf(stderr, "allocate helper port: %d\n", result);
		goto cleanup;
	}
	result = mach_port_insert_right(mach_task_self(), service, service,
		MACH_MSG_TYPE_MAKE_SEND);
	if (result != KERN_SUCCESS) {
		fprintf(stderr, "insert helper send right: %d\n", result);
		goto cleanup;
	}
	send_owned = 1;
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
	result = bootstrap_register(bootstrap_port, argv[1], service);
#pragma clang diagnostic pop
	if (result != KERN_SUCCESS) {
		fprintf(stderr, "register helper port: %d\n", result);
		goto cleanup;
	}
	puts("READY");
	fflush(stdout);
	result = mach_msg(&message.header, MACH_RCV_MSG | MACH_RCV_TIMEOUT,
		0, sizeof(message), service, 10000, MACH_PORT_NULL);
	if (result != MACH_MSG_SUCCESS) {
		fprintf(stderr, "receive helper request: %d\n", result);
		goto cleanup;
	}
	received = 1;
	if (message.header.msgh_id != 0x41495201 ||
		(message.header.msgh_bits & MACH_MSGH_BITS_COMPLEX) != 0 ||
		message.header.msgh_remote_port == MACH_PORT_NULL ||
		message.header.msgh_size <= sizeof(mach_msg_header_t) + 4U) {
		fputs("unexpected Mach request\n", stderr);
		goto cleanup;
	}
	puts("RECEIVED");
	fflush(stdout);
	do {
		count = read(STDIN_FILENO, &control, 1);
	} while (count < 0 && errno == EINTR);
	if (count != 0) {
		fputs("expected control EOF\n", stderr);
		goto cleanup;
	}
	status = EXIT_SUCCESS;

cleanup:
	if (received) {
		mach_msg_destroy(&message.header);
	}
	if (service != MACH_PORT_NULL) {
		if (send_owned) {
			(void)mach_port_deallocate(mach_task_self(), service);
		}
		(void)mach_port_mod_refs(mach_task_self(), service,
			MACH_PORT_RIGHT_RECEIVE, -1);
	}
	return status;
}
