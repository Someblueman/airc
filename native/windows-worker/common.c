#include "common.h"

#include <sddl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static HANDLE g_job;
static HANDLE g_watchdog_stop;
static HANDLE g_watchdog_thread;

static DWORD WINAPI watchdog_main(void *context) {
    HANDLE stop = (HANDLE)context;
    DWORD result = WaitForSingleObject(stop, 35000u);
    if (result != WAIT_OBJECT_0) {
        (void)TerminateProcess(GetCurrentProcess(), 1);
        ExitProcess(1);
    }
    return 0;
}

bool airc_start_watchdog(void) {
    HANDLE stop;
    HANDLE thread;
    if (g_watchdog_stop != NULL || g_watchdog_thread != NULL) return false;
    stop = CreateEventW(NULL, TRUE, FALSE, NULL);
    if (stop == NULL) return false;
    thread = CreateThread(NULL, 0, watchdog_main, stop, 0, NULL);
    if (thread == NULL) {
        CloseHandle(stop);
        return false;
    }
    g_watchdog_stop = stop;
    g_watchdog_thread = thread;
    return true;
}

void airc_stop_watchdog(void) {
    if (g_watchdog_stop == NULL || g_watchdog_thread == NULL) return;
    (void)SetEvent(g_watchdog_stop);
    (void)WaitForSingleObject(g_watchdog_thread, INFINITE);
    CloseHandle(g_watchdog_thread);
    CloseHandle(g_watchdog_stop);
    g_watchdog_thread = NULL;
    g_watchdog_stop = NULL;
}

bool airc_parse_sid(const char *value, PSID *sid_out) {
    LPWSTR canonical = NULL;
    PSID sid = NULL;
    int chars;
    bool valid = false;
    if (sid_out == NULL) return false;
    *sid_out = NULL;
    if (value == NULL || value[0] == '\0') return false;
    if (!ConvertStringSidToSidA(value, &sid)) return false;
    if (!IsValidSid(sid)) goto cleanup;
    if (!ConvertSidToStringSidW(sid, &canonical)) goto cleanup;
    chars = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, canonical, -1, NULL, 0, NULL, NULL);
    if (chars <= 0) goto cleanup;
    {
        char *text = (char *)malloc((size_t)chars);
        if (text == NULL) goto cleanup;
        if (WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, canonical, -1, text, chars, NULL, NULL) == chars && strcmp(text, value) == 0) {
            valid = true;
        }
        free(text);
    }
    if (valid) {
        *sid_out = sid;
        sid = NULL;
    }
cleanup:
    if (canonical != NULL) LocalFree(canonical);
    if (sid != NULL) LocalFree(sid);
    return valid;
}

bool airc_token_matches(PSID expected) {
    HANDLE token = NULL;
    DWORD needed = 0;
    union {
        TOKEN_USER alignment;
        BYTE bytes[sizeof(TOKEN_USER) + SECURITY_MAX_SID_SIZE];
    } storage;
    TOKEN_USER *user = (TOKEN_USER *)storage.bytes;
    bool matches = false;
    if (expected == NULL || !OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)) return false;
    if (GetTokenInformation(token, TokenUser, storage.bytes, (DWORD)sizeof(storage.bytes), &needed) &&
        needed >= sizeof(TOKEN_USER) && needed <= sizeof(storage.bytes)) {
        uintptr_t sid_address = (uintptr_t)user->User.Sid;
        uintptr_t begin = (uintptr_t)storage.bytes;
        uintptr_t end = begin + needed;
        if (sid_address >= begin && sid_address <= end && end - sid_address >= 8u) {
            DWORD sid_length = GetLengthSid((PSID)sid_address);
            if (sid_length != 0 && sid_length <= end - sid_address && IsValidSid((PSID)sid_address) &&
                EqualSid((PSID)sid_address, expected)) matches = true;
        }
    }
    if (!CloseHandle(token)) matches = false;
    return matches;
}

bool airc_stdio_is_piped(void) {
    HANDLE input = GetStdHandle(STD_INPUT_HANDLE);
    HANDLE output = GetStdHandle(STD_OUTPUT_HANDLE);
    return input != NULL && input != INVALID_HANDLE_VALUE && output != NULL && output != INVALID_HANDLE_VALUE &&
           GetFileType(input) == FILE_TYPE_PIPE && GetFileType(output) == FILE_TYPE_PIPE;
}

static bool read_exact(HANDLE input, uint8_t *buffer, uint32_t length) {
    uint32_t offset = 0;
    while (offset < length) {
        DWORD count = 0;
        DWORD remaining = (DWORD)(length - offset);
        if (!ReadFile(input, buffer + offset, remaining, &count, NULL) || count == 0) return false;
        offset += count;
    }
    return true;
}

static bool write_exact(HANDLE output, const uint8_t *buffer, uint32_t length) {
    uint32_t offset = 0;
    while (offset < length) {
        DWORD count = 0;
        DWORD remaining = (DWORD)(length - offset);
        if (!WriteFile(output, buffer + offset, remaining, &count, NULL) || count == 0) return false;
        offset += count;
    }
    return true;
}

bool airc_read_frame(HANDLE input, uint32_t maximum, uint8_t *buffer, uint32_t *length) {
    uint8_t header[4];
    uint32_t size;
    if (buffer == NULL || length == NULL || maximum == 0 || maximum > AIRC_MAX_RESPONSE) return false;
    if (!read_exact(input, header, sizeof(header))) return false;
    size = ((uint32_t)header[0] << 24) | ((uint32_t)header[1] << 16) | ((uint32_t)header[2] << 8) | (uint32_t)header[3];
    if (size == 0 || size > maximum) return false;
    if (!read_exact(input, buffer, size)) return false;
    *length = size;
    return true;
}

bool airc_write_frame(HANDLE output, const uint8_t *buffer, uint32_t length) {
    uint8_t header[4];
    if (buffer == NULL || length == 0 || length > AIRC_MAX_RESPONSE) return false;
    header[0] = (uint8_t)(length >> 24);
    header[1] = (uint8_t)(length >> 16);
    header[2] = (uint8_t)(length >> 8);
    header[3] = (uint8_t)length;
    return write_exact(output, header, sizeof(header)) && write_exact(output, buffer, length);
}

void airc_diagnostic(const char *message) {
    if (message != NULL) {
        (void)fprintf(stderr, "windows-worker: %s\n", message);
        (void)fflush(stderr);
    }
}

bool airc_apply_job_limits(void) {
    JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits;
    HANDLE job;
    if (g_job != NULL) return false;
    job = CreateJobObjectW(NULL, NULL);
    if (job == NULL) return false;
    ZeroMemory(&limits, sizeof(limits));
    limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
        JOB_OBJECT_LIMIT_PROCESS_MEMORY | JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    limits.BasicLimitInformation.ActiveProcessLimit = 1;
    limits.ProcessMemoryLimit = (SIZE_T)(64u * 1024u * 1024u);
    if (!SetInformationJobObject(job, JobObjectExtendedLimitInformation, &limits, (DWORD)sizeof(limits)) ||
        !AssignProcessToJobObject(job, GetCurrentProcess())) {
        CloseHandle(job);
        return false;
    }
    g_job = job;
    return true;
}

