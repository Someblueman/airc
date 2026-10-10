/* SPDX-License-Identifier: BSD-2-Clause */
#include "ipc.h"
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <sys/socket.h>
#include <time.h>
int airc_oscore_socket(int fd) {
  int flags = fcntl(fd, F_GETFL);
  if (flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) != 0)
    return 0;
#ifdef SO_NOSIGPIPE
  int enabled = 1;
  if (setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &enabled, sizeof(enabled)) != 0)
    return 0;
#endif
  return 1;
}

int64_t airc_oscore_now(void) {
  struct timespec now;
  if (clock_gettime(CLOCK_MONOTONIC, &now) != 0)
    return -1;
  return (int64_t)now.tv_sec * 1000 + now.tv_nsec / 1000000;
}

int airc_oscore_io(int fd, uint8_t *bytes, size_t length, int write_data,
                    int64_t deadline) {
  while (length != 0) {
    int64_t now = airc_oscore_now();
    struct pollfd readiness = {fd, write_data ? POLLOUT : POLLIN, 0};
    ssize_t count;
    int ready;
    if (now < 0 || now >= deadline)
      return 0;
    ready = poll(&readiness, 1, (int)(deadline-now));
    if (ready < 0 && errno == EINTR)
      continue;
    if (ready <= 0)
      return 0;
    if (write_data) {
#ifdef MSG_NOSIGNAL
      count = send(fd, bytes, length, MSG_NOSIGNAL);
#else
      count = send(fd, bytes, length, 0);
#endif
    } else {
      count = recv(fd, bytes, length, 0);
    }
    if (count < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK))
      continue;
    if (count <= 0)
      return 0;
    bytes += count;
    length -= (size_t)count;
  }
  return 1;
}

