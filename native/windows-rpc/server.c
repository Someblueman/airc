#include "common.h"
#include "gateway.h"

#include <sddl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define AIRC_SERVER_TIMEOUT_MS 30000u

static PSID g_server_sid;
static PSID g_client_sid;
static HANDLE g_finished;
static HANDLE g_ready;
static volatile LONG g_call_claimed = 0;
static volatile LONG g_call_status = RPC_S_OK;
static bool g_interface_registered;
static bool g_endpoint_registered;
static bool g_listening;

static bool sid_matches_impersonated_client(handle_t binding) {
    HANDLE token = NULL;
    union {
        TOKEN_USER alignment;
        BYTE bytes[sizeof(TOKEN_USER) + SECURITY_MAX_SID_SIZE];
    } storage;
    TOKEN_USER *user = (TOKEN_USER *)storage.bytes;
    DWORD needed = 0;
    bool match = false;
    if (RpcImpersonateClient(binding) != RPC_S_OK) return false;
    if (OpenThreadToken(GetCurrentThread(), TOKEN_QUERY, TRUE, &token)) {
        if (GetTokenInformation(token, TokenUser, storage.bytes, (DWORD)sizeof(storage.bytes), &needed) &&
            needed >= sizeof(TOKEN_USER) && needed <= sizeof(storage.bytes) &&
            IsValidSid(user->User.Sid) && EqualSid(user->User.Sid, g_client_sid)) {
            match = true;
        }
        if (!CloseHandle(token)) match = false;
    }
    if (RpcRevertToSelf() != RPC_S_OK) {
        (void)TerminateProcess(GetCurrentProcess(), 1);
        ExitProcess(1);
    }
    return match;
}

static RPC_STATUS RPC_ENTRY authenticate_call(RPC_IF_HANDLE interface_handle, void *context) {
    RPC_BINDING_HANDLE binding = (RPC_BINDING_HANDLE)context;
    unsigned long level = 0;
    unsigned long authn = 0;
    unsigned long authz = 0;
    RPC_STATUS status;
    (void)interface_handle;
    if (binding == NULL) return RPC_S_ACCESS_DENIED;
    status = RpcBindingInqAuthClientW(binding, NULL, NULL, &level, &authn, &authz);
    if (status != RPC_S_OK || authn != RPC_C_AUTHN_WINNT || level != RPC_C_AUTHN_LEVEL_PKT_PRIVACY ||
        authz != RPC_C_AUTHZ_NONE) return RPC_S_ACCESS_DENIED;
    if (!sid_matches_impersonated_client(binding)) return RPC_S_ACCESS_DENIED;
    return RPC_S_OK;
}

error_status_t Exchange(handle_t binding, unsigned long request_size, byte request[], unsigned long *response_size, byte response[]) {
    HANDLE input = GetStdHandle(STD_INPUT_HANDLE);
    HANDLE output = GetStdHandle(STD_OUTPUT_HANDLE);
    uint32_t result_size = 0;
    RPC_STATUS status = RPC_S_OK;
    if (response_size != NULL) *response_size = 0;
    if (InterlockedCompareExchange(&g_call_claimed, 1, 0) != 0) return RPC_S_SERVER_TOO_BUSY;
    if (response_size == NULL || response == NULL || request == NULL || request_size == 0 || request_size > AIRC_MAX_REQUEST) {
        status = RPC_S_INVALID_ARG;
        goto done;
    }
    if (!sid_matches_impersonated_client(binding)) {
        status = RPC_S_ACCESS_DENIED;
        goto done;
    }
    // An early caller must not interleave its request with the readiness record.
    if (WaitForSingleObject(g_ready, 5000u) != WAIT_OBJECT_0) {
        status = RPC_S_CALL_FAILED;
        goto done;
    }
    if (!airc_write_frame(output, request, (uint32_t)request_size)) {
        status = RPC_S_CALL_FAILED;
        goto done;
    }
    if (!airc_read_frame(input, AIRC_MAX_RESPONSE, response, &result_size)) {
        status = RPC_S_CALL_FAILED;
        goto done;
    }
    *response_size = result_size;
done:
    (void)InterlockedExchange(&g_call_status, (LONG)status);
    if (g_finished != NULL) (void)SetEvent(g_finished);
    return status;
}

int main(int argc, char **argv) {
    int exit_code = 1;
    char sddl[512];
    int n;
    PSECURITY_DESCRIPTOR sd = NULL;
    RPC_BINDING_VECTOR *bindings = NULL;
    RPC_STATUS status;
    WCHAR annotation[] = L"AIRC local RPC gateway worker";
    HANDLE finished = NULL;
    DWORD wait_result;
    unsigned long rpc_flags = RPC_IF_ALLOW_LOCAL_ONLY | RPC_IF_ALLOW_SECURE_ONLY | RPC_IF_SEC_NO_CACHE;
    if (!airc_apply_job_limits()) {
        airc_diagnostic("could not establish worker job limits");
        goto cleanup;
    }
    if (!airc_start_watchdog()) {
        airc_diagnostic("could not start worker watchdog");
        goto cleanup;
    }
    if (argc != 3 || !airc_stdio_is_piped()) {
        airc_diagnostic("invalid arguments or stdio is not pipe-backed");
        goto cleanup;
    }
    if (!airc_parse_sid(argv[1], &g_server_sid) || !airc_parse_sid(argv[2], &g_client_sid) || !airc_token_matches(g_server_sid)) {
        airc_diagnostic("worker SID validation failed");
        goto cleanup;
    }
    n = snprintf(sddl, sizeof(sddl), "O:%sD:P(A;;GA;;;%s)(A;;GA;;;%s)", argv[1], argv[1], argv[2]);
    if (n <= 0 || (size_t)n >= sizeof(sddl) ||
        !ConvertStringSecurityDescriptorToSecurityDescriptorA(sddl, SDDL_REVISION_1, &sd, NULL)) {
        airc_diagnostic("could not build RPC interface security descriptor");
        goto cleanup;
    }
    finished = CreateEventW(NULL, TRUE, FALSE, NULL);
    if (finished == NULL) goto cleanup;
    g_finished = finished;
    g_ready = CreateEventW(NULL, TRUE, FALSE, NULL);
    if (g_ready == NULL) goto cleanup;
    status = RpcServerRegisterAuthInfoW(NULL, RPC_C_AUTHN_WINNT, NULL, NULL);
    if (status != RPC_S_OK) goto rpc_failure;
    status = RpcServerUseProtseqW((RPC_WSTR)L"ncalrpc", RPC_C_PROTSEQ_MAX_REQS_DEFAULT, NULL);
    if (status != RPC_S_OK) goto rpc_failure;
    status = RpcServerRegisterIf3(gateway_v1_0_s_ifspec, NULL, NULL, rpc_flags, 1, AIRC_MAX_REQUEST,
                                  authenticate_call, sd);
    if (status != RPC_S_OK) goto rpc_failure;
    g_interface_registered = true;
    status = RpcServerInqBindings(&bindings);
    if (status != RPC_S_OK) goto rpc_failure;
    status = RpcEpRegisterW(gateway_v1_0_s_ifspec, bindings, NULL, annotation);
    if (status != RPC_S_OK) goto rpc_failure;
    g_endpoint_registered = true;
    status = RpcServerListen(1, 1, TRUE);
    if (status != RPC_S_OK) goto rpc_failure;
    g_listening = true;
    {
        const uint8_t ready = 1;
        if (!airc_write_frame(GetStdHandle(STD_OUTPUT_HANDLE), &ready, sizeof(ready))) {
            airc_diagnostic("could not publish worker readiness");
            goto cleanup;
        }
        if (!SetEvent(g_ready)) goto cleanup;
    }
    wait_result = WaitForSingleObject(finished, AIRC_SERVER_TIMEOUT_MS);
    if (wait_result != WAIT_OBJECT_0) {
        airc_diagnostic("RPC request did not finish before its deadline");
        (void)TerminateProcess(GetCurrentProcess(), 1);
        ExitProcess(1);
    }
    if (InterlockedCompareExchange(&g_call_status, 0, 0) == RPC_S_OK) exit_code = 0;
    goto cleanup;
rpc_failure:
    airc_diagnostic("RPC server setup or shutdown failed");
cleanup:
    if (g_listening) {
        if (RpcMgmtStopServerListening(NULL) != RPC_S_OK) exit_code = 1;
        g_listening = false;
    }
    if (g_endpoint_registered) {
        if (RpcEpUnregister(gateway_v1_0_s_ifspec, bindings, NULL) != RPC_S_OK) exit_code = 1;
        g_endpoint_registered = false;
    }
    if (g_interface_registered) {
        if (RpcServerUnregisterIf(gateway_v1_0_s_ifspec, NULL, TRUE) != RPC_S_OK) exit_code = 1;
        g_interface_registered = false;
    }
    g_finished = NULL;
    if (g_ready != NULL && !CloseHandle(g_ready)) exit_code = 1;
    g_ready = NULL;
    if (bindings != NULL && RpcBindingVectorFree(&bindings) != RPC_S_OK) exit_code = 1;
    if (finished != NULL && !CloseHandle(finished)) exit_code = 1;
    if (sd != NULL) LocalFree(sd);
    if (g_server_sid != NULL) LocalFree(g_server_sid);
    if (g_client_sid != NULL) LocalFree(g_client_sid);
    airc_stop_watchdog();
    return exit_code;
}
