#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#define OBJECT_BYTES 16384
#define NAME_PREFIX "/agw-shm-"
#define NAME_LENGTH 31
#define SUFFIX_HEX_LENGTH (NAME_LENGTH - (sizeof(NAME_PREFIX) - 1))

_Static_assert(SUFFIX_HEX_LENGTH == 22, "fixture suffix must fit the shm name limit");

static int valid_name(const char *name)
{
    size_t i;

    if (name == NULL || strlen(name) != NAME_LENGTH ||
        strncmp(name, NAME_PREFIX, sizeof(NAME_PREFIX) - 1) != 0) {
        return 0;
    }
    for (i = sizeof(NAME_PREFIX) - 1; i < NAME_LENGTH; ++i) {
        const char c = name[i];
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))) {
            return 0;
        }
    }
    return 1;
}

static void report_error(const char *operation, int error_number)
{
    (void)fprintf(stderr, "shared-memory helper %s: %s\n", operation,
                  strerror(error_number));
}

static int create_object(const char *name)
{
    int fd = -1;
    int created = 0;
    int result = EXIT_FAILURE;
    int saved_error = 0;
    void *mapping = MAP_FAILED;
    struct stat info;

    (void)umask(0);
    fd = shm_open(name, O_RDWR | O_CREAT | O_EXCL, 0600);
    if (fd < 0) {
        saved_error = errno;
        report_error("shm_open", saved_error);
        goto cleanup;
    }
    created = 1;
    if (ftruncate(fd, OBJECT_BYTES) < 0) {
        saved_error = errno;
        report_error("ftruncate", saved_error);
        goto cleanup;
    }
    if (fstat(fd, &info) < 0) {
        saved_error = errno;
        report_error("fstat", saved_error);
        goto cleanup;
    }
#if defined(__APPLE__)
    if (info.st_uid != geteuid() || (info.st_mode & 07777) != 0600 ||
        info.st_size != OBJECT_BYTES) {
#else
    if (!S_ISREG(info.st_mode) || info.st_uid != geteuid() ||
        (info.st_mode & 07777) != 0600 || info.st_size != OBJECT_BYTES) {
#endif
        saved_error = EINVAL;
        report_error("verify object metadata", saved_error);
        goto cleanup;
    }
    mapping = mmap(NULL, OBJECT_BYTES, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
    if (mapping == MAP_FAILED) {
        saved_error = errno;
        report_error("mmap", saved_error);
        goto cleanup;
    }
    memset(mapping, 0, OBJECT_BYTES);
    memcpy((unsigned char *)mapping + 4, "AGQ1", 4);
    result = EXIT_SUCCESS;

cleanup:
    if (mapping != MAP_FAILED && munmap(mapping, OBJECT_BYTES) < 0) {
        if (result == EXIT_SUCCESS) {
            saved_error = errno;
            report_error("munmap", saved_error);
            result = EXIT_FAILURE;
        }
    }
    if (fd >= 0 && close(fd) < 0 && result == EXIT_SUCCESS) {
        saved_error = errno;
        report_error("close", saved_error);
        result = EXIT_FAILURE;
    }
    if (result != EXIT_SUCCESS && created && shm_unlink(name) < 0 && errno != ENOENT) {
        report_error("cleanup shm_unlink", errno);
    }
    return result;
}

static int unlink_object(const char *name)
{
    if (shm_unlink(name) < 0) {
        report_error("shm_unlink", errno);
        return EXIT_FAILURE;
    }
    return EXIT_SUCCESS;
}

int main(int argc, char **argv)
{
    if (argc != 3 || !valid_name(argv[2])) {
        (void)fprintf(stderr, "usage: shm-fixture {create|unlink} /agw-shm-<22 lowercase hex>\n");
        return EXIT_FAILURE;
    }
    if (strcmp(argv[1], "create") == 0) {
        return create_object(argv[2]);
    }
    if (strcmp(argv[1], "unlink") == 0) {
        return unlink_object(argv[2]);
    }
    (void)fprintf(stderr, "unknown operation\n");
    return EXIT_FAILURE;
}
