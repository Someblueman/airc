#define _POSIX_C_SOURCE 200809L

#include <arpa/inet.h>
#include <dds/dds.h>
#include <errno.h>
#include <ifaddrs.h>
#include <inttypes.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/resource.h>
#include <time.h>
#include <unistd.h>

#include "AIRC.h"

#define MAX_PAYLOAD 32768u
#define DDS_TOPIC "AIRC_SignedBundle_v1"
#define DDS_PARTITION "AIRC/gateway/v1"

static int set_limit(int resource, rlim_t maximum)
{
  struct rlimit limit;
  if (getrlimit(resource, &limit) != 0)
    return -1;
  if (limit.rlim_max != RLIM_INFINITY && limit.rlim_max < maximum)
    maximum = limit.rlim_max;
  limit.rlim_cur = maximum;
  limit.rlim_max = maximum;
  return setrlimit(resource, &limit);
}

static int install_limits(void)
{
  struct rlimit core = {0, 0};
  if (set_limit(RLIMIT_AS, 512u * 1024u * 1024u) != 0 ||
      set_limit(RLIMIT_NOFILE, 64) != 0 ||
      setrlimit(RLIMIT_CORE, &core) != 0)
    return -1;
  return 0;
}

static int parse_domain(const char *text, uint32_t *domain)
{
  char *end = NULL;
  unsigned long value;
  if (text == NULL || text[0] == '\0' || (text[0] == '0' && text[1] != '\0'))
    return -1;
  for (const unsigned char *p = (const unsigned char *) text; *p != '\0'; p++)
    if (*p < '0' || *p > '9')
      return -1;
  errno = 0;
  value = strtoul(text, &end, 10);
  if (errno != 0 || end == text || *end != '\0' || value > 230)
    return -1;
  *domain = (uint32_t) value;
  return 0;
}

static int parse_ipv4(const char *text, struct in_addr *address)
{
  char canonical[INET_ADDRSTRLEN];
  uint32_t host;
  if (text == NULL || inet_pton(AF_INET, text, address) != 1 ||
      inet_ntop(AF_INET, address, canonical, sizeof(canonical)) == NULL ||
      strcmp(text, canonical) != 0)
    return -1;
  host = ntohl(address->s_addr);
  if (host == INADDR_ANY || host == INADDR_BROADCAST ||
      (host & 0xf0000000u) == 0xe0000000u)
    return -1;
  return 0;
}

/* Cyclone also accepts network-prefix selectors. Require one actual interface
 * address first so an unassigned network address cannot select another host. */
static int local_interface(const struct in_addr *address)
{
  struct ifaddrs *interfaces = NULL;
  unsigned matches = 0;
  if (getifaddrs(&interfaces) != 0)
    return -1;
  for (const struct ifaddrs *it = interfaces; it != NULL; it = it->ifa_next) {
    if (it->ifa_addr != NULL && it->ifa_addr->sa_family == AF_INET) {
      const struct sockaddr_in *ip = (const struct sockaddr_in *) it->ifa_addr;
      if (ip->sin_addr.s_addr == address->s_addr)
        matches++;
    }
  }
  freeifaddrs(interfaces);
  return matches == 1 ? 0 : -1;
}

static int deadline_after(struct timespec *deadline, unsigned seconds)
{
  if (clock_gettime(CLOCK_MONOTONIC, deadline) != 0)
    return -1;
  deadline->tv_sec += (time_t) seconds;
  return 0;
}

static int deadline_expired(const struct timespec *deadline)
{
  struct timespec now;
  if (clock_gettime(CLOCK_MONOTONIC, &now) != 0)
    return 1;
  return now.tv_sec > deadline->tv_sec ||
         (now.tv_sec == deadline->tv_sec && now.tv_nsec >= deadline->tv_nsec);
}

static void short_pause(void)
{
  const struct timespec pause = {0, 20 * 1000 * 1000};
  while (nanosleep(&pause, NULL) != 0 && errno == EINTR)
    ;
}

static int configure_domain(uint32_t domain, const char *local, const char *peer,
                            char *xml, size_t xml_capacity)
{
  int n = snprintf(xml, xml_capacity,
      "<CycloneDDS><Domain Id=\"%" PRIu32 "\">"
      "<General><Transport>udp</Transport><Interfaces>"
      "<NetworkInterface address=\"%s\" multicast=\"false\"/>"
      "</Interfaces><AllowMulticast>false</AllowMulticast></General>"
      "<Discovery><ParticipantIndex>auto</ParticipantIndex>"
      "<MaxAutoParticipantIndex>1</MaxAutoParticipantIndex>"
      "<Peers><Peer Address=\"%s\"/></Peers></Discovery>"
      "</Domain></CycloneDDS>", domain, local, peer);
  return n < 0 || (size_t) n >= xml_capacity ? -1 : 0;
}

static int make_qos(dds_qos_t **qos)
{
  *qos = dds_create_qos();
  if (*qos == NULL)
    return -1;
  dds_qset_durability(*qos, DDS_DURABILITY_VOLATILE);
  dds_qset_reliability(*qos, DDS_RELIABILITY_RELIABLE, DDS_SECS(2));
  dds_qset_history(*qos, DDS_HISTORY_KEEP_LAST, 1);
  dds_qset_resource_limits(*qos, 1, 1, 1);
  return 0;
}

static int make_partition_qos(dds_qos_t **qos)
{
  *qos = dds_create_qos();
  if (*qos == NULL)
    return -1;
  dds_qset_partition1(*qos, DDS_PARTITION);
  return 0;
}

static int read_input(AIRC_SignedBundleV1 *sample)
{
  size_t n;
  int extra;
  memset(sample, 0, sizeof(*sample));
  n = fread(sample->payload, 1, MAX_PAYLOAD, stdin);
  if (ferror(stdin) || n == 0)
    return -1;
  extra = fgetc(stdin);
  if (extra != EOF || ferror(stdin))
    return -1;
  sample->length = (uint32_t) n;
  return 0;
}

static int zero_padding(const AIRC_SignedBundleV1 *sample)
{
  if (sample->length == 0 || sample->length > MAX_PAYLOAD)
    return -1;
  for (size_t i = sample->length; i < MAX_PAYLOAD; i++)
    if (sample->payload[i] != 0)
      return -1;
  return 0;
}

static int write_output(const uint8_t *data, size_t length)
{
  size_t written = 0;
  while (written < length) {
    ssize_t n = write(STDOUT_FILENO, data + written, length - written);
    if (n < 0 && errno == EINTR)
      continue;
    if (n <= 0)
      return -1;
    written += (size_t) n;
  }
  return 0;
}

static dds_duration_t remaining_duration(const struct timespec *deadline)
{
  struct timespec now;
  int64_t seconds;
  int64_t nanoseconds;
  if (clock_gettime(CLOCK_MONOTONIC, &now) != 0)
    return 0;
  seconds = (int64_t) deadline->tv_sec - (int64_t) now.tv_sec;
  nanoseconds = (int64_t) deadline->tv_nsec - (int64_t) now.tv_nsec;
  if (nanoseconds < 0) {
    seconds--;
    nanoseconds += 1000000000;
  }
  if (seconds < 0 || (seconds == 0 && nanoseconds == 0))
    return 0;
  return (dds_duration_t) (seconds * INT64_C(1000000000) + nanoseconds);
}

int main(int argc, char **argv)
{
  uint32_t domain_id;
  struct in_addr local_address, peer_address;
  char xml[2048];
  AIRC_SignedBundleV1 sample;
  dds_entity_t domain_entity = DDS_ENTITY_NIL;
  dds_entity_t participant = DDS_ENTITY_NIL;
  dds_entity_t topic = DDS_ENTITY_NIL;
  dds_entity_t publisher = DDS_ENTITY_NIL;
  dds_entity_t subscriber = DDS_ENTITY_NIL;
  dds_entity_t writer = DDS_ENTITY_NIL;
  dds_entity_t reader = DDS_ENTITY_NIL;
  dds_qos_t *qos = NULL;
  int result = EXIT_FAILURE;
  int read_mode;
  size_t output_length = 0;
  struct timespec deadline;

  if (argc != 5 || (strcmp(argv[1], "read") != 0 && strcmp(argv[1], "emit") != 0) ||
      install_limits() != 0 || parse_domain(argv[2], &domain_id) != 0 ||
      parse_ipv4(argv[3], &local_address) != 0 ||
      parse_ipv4(argv[4], &peer_address) != 0 || local_address.s_addr == peer_address.s_addr)
    return EXIT_FAILURE;
  read_mode = strcmp(argv[1], "read") == 0;
  /* dds_take writes into caller-owned samples; initialize the fixed sample. */
  memset(&sample, 0, sizeof(sample));
  if (signal(SIGALRM, SIG_DFL) == SIG_ERR)
    goto cleanup;
  alarm(30);
  if (deadline_after(&deadline, 25) != 0 || local_interface(&local_address) != 0)
    goto cleanup;
  if (!read_mode && read_input(&sample) != 0)
    goto cleanup;
  if (unsetenv("CYCLONEDDS_URI") != 0 ||
      configure_domain(domain_id, argv[3], argv[4], xml, sizeof(xml)) != 0)
    goto cleanup;

  domain_entity = dds_create_domain(domain_id, xml);
  if (domain_entity < 0)
    goto cleanup;
  participant = dds_create_participant(domain_id, NULL, NULL);
  if (participant < 0)
    goto cleanup;
  if (make_qos(&qos) != 0)
    goto cleanup;
  topic = dds_create_topic(participant, &AIRC_SignedBundleV1_desc, DDS_TOPIC, qos, NULL);
  dds_delete_qos(qos);
  qos = NULL;
  if (topic < 0)
    goto cleanup;

  if (read_mode) {
    if (make_partition_qos(&qos) != 0)
      goto cleanup;
    subscriber = dds_create_subscriber(participant, qos, NULL);
    dds_delete_qos(qos);
    qos = NULL;
    if (subscriber < 0)
      goto cleanup;
    if (make_qos(&qos) != 0)
      goto cleanup;
    reader = dds_create_reader(subscriber, topic, qos, NULL);
    dds_delete_qos(qos);
    qos = NULL;
    if (reader < 0)
      goto cleanup;
    for (;;) {
      void *samples[1] = {&sample};
      dds_sample_info_t info;
      dds_return_t count = dds_take(reader, samples, &info, 1, 1);
      if (count < 0)
        goto cleanup;
      if (count == 1) {
        if (!info.valid_data || zero_padding(&sample) != 0)
          goto cleanup;
        output_length = sample.length;
        break;
      }
      if (deadline_expired(&deadline))
        goto cleanup;
      short_pause();
    }
  } else {
    dds_publication_matched_status_t matched;
    if (make_partition_qos(&qos) != 0)
      goto cleanup;
    publisher = dds_create_publisher(participant, qos, NULL);
    dds_delete_qos(qos);
    qos = NULL;
    if (publisher < 0)
      goto cleanup;
    if (make_qos(&qos) != 0)
      goto cleanup;
    writer = dds_create_writer(publisher, topic, qos, NULL);
    dds_delete_qos(qos);
    qos = NULL;
    if (writer < 0 || dds_set_status_mask(writer, DDS_PUBLICATION_MATCHED_STATUS) < 0)
      goto cleanup;
    for (;;) {
      if (dds_get_publication_matched_status(writer, &matched) < 0)
        goto cleanup;
      if (matched.current_count > 0)
        break;
      if (deadline_expired(&deadline))
        goto cleanup;
      short_pause();
    }
    {
      dds_duration_t remaining = remaining_duration(&deadline);
      if (remaining == 0 || dds_write(writer, &sample) < 0 ||
          dds_wait_for_acks(writer, remaining) < 0)
        goto cleanup;
    }
  }
  result = EXIT_SUCCESS;

cleanup:
  if (qos != NULL)
    dds_delete_qos(qos);
  if (reader > 0 && dds_delete(reader) < 0)
    result = EXIT_FAILURE;
  if (subscriber > 0 && dds_delete(subscriber) < 0)
    result = EXIT_FAILURE;
  if (writer > 0 && dds_delete(writer) < 0)
    result = EXIT_FAILURE;
  if (publisher > 0 && dds_delete(publisher) < 0)
    result = EXIT_FAILURE;
  if (topic > 0 && dds_delete(topic) < 0)
    result = EXIT_FAILURE;
  if (participant > 0 && dds_delete(participant) < 0)
    result = EXIT_FAILURE;
  if (domain_entity > 0 && dds_delete(domain_entity) < 0)
    result = EXIT_FAILURE;
  if (result == EXIT_SUCCESS && read_mode && write_output(sample.payload, output_length) != 0)
    result = EXIT_FAILURE;
  return result;
}
